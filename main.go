package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/urfave/cli"
	"golang.org/x/mod/modfile"
)

type goModInfo struct {
	GoVersion string
	Deps      dependencies
}

// key=module path, value=version
type dependencies map[string]string

func parseGoMod(r io.Reader) (*modfile.File, error) {
	data, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	file, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return nil, err
	}

	return file, nil
}

func parseIgnoreFile(path string, ignored map[string]struct{}) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		module := scanner.Text()
		if module == "" {
			continue
		}
		ignored[module] = struct{}{}
	}

	return nil
}

func localDependencies(dir string) (goModInfo, error) {
	info := goModInfo{
		Deps: dependencies{},
	}

	goModFile, err := os.Open(filepath.Join(dir, "go.mod"))
	if err != nil {
		return info, err
	}
	defer goModFile.Close()

	f, err := parseGoMod(goModFile)
	if err != nil {
		return info, err
	}

	info.GoVersion = f.Go.Version

	cmd := exec.Command("go", "list", "-m", "all")
	cmd.Dir = dir
	data, err := cmd.CombinedOutput()
	if err != nil {
		return info, err
	}

	deps := make(map[string]struct{})
	for _, required := range f.Require {
		deps[required.Mod.Path] = struct{}{}
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		// Ignore dependencies that are not in the go.mod. This can
		// happen for indirect deps of indirect deps. Since these don't
		// appear in the Go mod, there's little we can do to pin to a
		// correct version
		module, version := fields[0], fields[1]
		if _, found := deps[module]; !found {
			continue
		}

		info.Deps[module] = version
	}

	return info, nil
}

func k8sDependencies(version string) (goModInfo, error) {
	info := goModInfo{
		Deps: dependencies{},
	}
	resp, err := http.Get(fmt.Sprintf("https://raw.githubusercontent.com/kubernetes/kubernetes/%s/go.mod", version))
	if err != nil {
		return info, err
	}
	defer resp.Body.Close()

	f, err := parseGoMod(resp.Body)
	if err != nil {
		return info, err
	}

	info.GoVersion = f.Go.Version

	// Kubernetes's go.mod contains a bunch of replace that targets local
	// path. We want to skip those.
	// eg:
	//     require (
	//          k8s.io/api v0.0.0
	//          ...
	//     )
	//
	//     replace (
	//          k8s.io/api => ./staging/src/k8s.io/api
	//          ...
	//     )
	replacements := make(map[string]struct{})
	for _, replaced := range f.Replace {
		replacements[replaced.Old.Path] = struct{}{}
	}

	for _, required := range f.Require {
		// We skip the indirect dependencies because they're not used
		// by k8s so they shouldn't cause incompatibility issues
		if required.Indirect {
			continue
		}
		if _, exists := replacements[required.Mod.Path]; exists {
			continue
		}

		info.Deps[required.Mod.Path] = required.Mod.Version
	}

	return info, nil
}

type renovateConfig struct {
	PackageRules []packageRule `json:"packageRules"`
}

type packageRule struct {
	MatchPackageNames []string `json:"matchPackageNames"`
	AllowedVersions   string   `json:"allowedVersions"`
}

func writeJSON(output string, v any) error {
	outputTemp := output + ".tmp"
	file, err := os.Create(outputTemp)
	if err != nil {
		return err
	}

	if err := json.NewEncoder(file).Encode(v); err != nil {
		return err
	}

	if err := os.Rename(outputTemp, output); err != nil {
		return err
	}
	return nil
}

func getK8sVersion(version string, info goModInfo) (string, error) {
	if version != "auto" {
		return version, nil
	}

	// One of these should be in the go.mod
	modules := []string{
		"k8s.io/api",
		"k8s.io/apimachinery",
		"k8s.io/client-go",
	}
	for _, module := range modules {
		if k8sVersion, exists := info.Deps[module]; exists {
			// Convert from v0.X.Y to v1.X.Y because libraries are
			// v0 based
			return strings.Replace(k8sVersion, "v0", "v1", 1), nil
		}
	}

	return "", fmt.Errorf("couldn't detect k8s version")
}

func updateRenovate() cli.Command {
	return cli.Command{
		Name:      "update-renovate",
		Usage:     "Update the renovate config with pinned dependencies from k8s upstream",
		ArgsUsage: "[directory]",
		Action: func(ctx *cli.Context) error {
			local, err := localDependencies(ctx.Args().First())
			if err != nil {
				return err
			}
			k8sVersion, err := getK8sVersion(ctx.String("k8s-version"), local)
			if err != nil {
				return err
			}

			k8s, err := k8sDependencies(k8sVersion)
			if err != nil {
				return err
			}

			ignored := make(map[string]struct{})
			ignoreFile := ctx.String("ignore-file")
			if ignoreFile != "" {
				if err = parseIgnoreFile(ignoreFile, ignored); err != nil {
					return fmt.Errorf("parsing ignore-file: %w", err)
				}
			}

			config := renovateConfig{
				PackageRules: make([]packageRule, 0),
			}

			for module, ver := range k8s.Deps {
				_, isIgnored := ignored[module]
				if isIgnored {
					continue
				}

				log.Printf(`Pinning %q to %q\n`, module, ver)
				rule := packageRule{
					MatchPackageNames: []string{module},
					AllowedVersions:   ver,
				}
				config.PackageRules = append(config.PackageRules, rule)
			}

			sort.Slice(config.PackageRules, func(i, j int) bool {
				return config.PackageRules[i].MatchPackageNames[0] < config.PackageRules[j].MatchPackageNames[0]
			})

			output := ctx.String("output")
			if ctx.Bool("merge") {
				var file *os.File
				data := make(map[string]any)
				file, err = os.Open(output)
				if err != nil {
					return err
				}

				if err = json.NewDecoder(file).Decode(&data); err != nil {
					return err
				}

				var rules []any
				_, ok := data["packageRules"]
				if ok {
					rules = data["packageRules"].([]any)
				} else {
					rules = make([]any, 0)
				}

				for _, rule := range config.PackageRules {
					rules = append(rules, rule)
				}
				data["packageRules"] = rules
				if err := writeJSON(output, data); err != nil {
					return err
				}
			} else {
				if err := writeJSON(output, config); err != nil {
					return err
				}
			}

			return nil
		},
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "k8s-version",
				Usage: "The k8s version to look for",
				Value: "auto",
			},
			cli.StringFlag{
				Name:  "ignore-file",
				Usage: "A file with ignore lines",
			},
			cli.StringFlag{
				Name:     "output",
				Usage:    "Path to the json output",
				Required: true,
			},
			cli.BoolFlag{
				Name:  "merge",
				Usage: "If true, will merge with existing file",
			},
		},
	}
}

func checkCmd() cli.Command {
	return cli.Command{
		Name:      "check",
		Usage:     "Check that dependencies and Go version from upstream k8s are pinned to the correct version",
		ArgsUsage: "[directory]",
		Action: func(ctx *cli.Context) error {
			local, err := localDependencies(ctx.Args().First())
			if err != nil {
				return err
			}
			k8sVersion, err := getK8sVersion(ctx.String("k8s-version"), local)
			if err != nil {
				return err
			}
			k8s, err := k8sDependencies(k8sVersion)
			if err != nil {
				return err
			}

			if local.GoVersion != k8s.GoVersion {
				log.Printf("Go version is different, local=%s vs upstream=%s\n", local.GoVersion, k8s.GoVersion)
			}

			ignored := make(map[string]struct{})
			ignoreFile := ctx.String("ignore-file")
			if ignoreFile != "" {
				if err = parseIgnoreFile(ignoreFile, ignored); err != nil {
					return fmt.Errorf("parsing ignore-file: %w", err)
				}
			}

			type modDiff struct {
				Path            string
				LocalVersion    string
				UpstreamVersion string
			}

			differences := []modDiff{}
			for module, kver := range k8s.Deps {
				lver, exists := local.Deps[module]
				if !exists {
					continue
				}

				_, isIgnored := ignored[module]
				if isIgnored {
					continue
				}

				if kver != lver {
					differences = append(differences, modDiff{
						Path:            module,
						LocalVersion:    lver,
						UpstreamVersion: kver,
					})
				}
			}

			sort.Slice(differences, func(i, j int) bool {
				return differences[i].Path < differences[j].Path
			})

			if len(differences) > 0 {
				for _, diff := range differences {
					log.Printf("Module %q is different, local=%s vs upstream=%s\n", diff.Path, diff.LocalVersion, diff.UpstreamVersion)
				}
				return fmt.Errorf("some dependencies are not pinned to k8s upstream's version")
			}

			return nil
		},
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "k8s-version",
				Usage: "The k8s version to look for",
				Value: "auto",
			},
			cli.StringFlag{
				Name:  "ignore-file",
				Usage: "A file with ignore lines",
			},
		},
	}
}

func main() {
	app := cli.NewApp()
	app.Commands = []cli.Command{
		checkCmd(),
		updateRenovate(),
	}
	if err := app.Run(os.Args); err != nil {
		log.Fatal(err)
	}
}
