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
	"sort"
	"strings"

	"github.com/urfave/cli"
	"golang.org/x/mod/modfile"
)

type dependencies map[string]string

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

func localDependencies() (dependencies, error) {
	deps := dependencies{}

	cmd := exec.Command("go", "list", "-m", "all")
	data, err := cmd.CombinedOutput()
	if err != nil {
		return deps, err
	}

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		deps[fields[0]] = fields[1]
	}
	return deps, nil
}

func k8sDependencies(version string) (dependencies, error) {
	deps := dependencies{}
	resp, err := http.Get(fmt.Sprintf("https://raw.githubusercontent.com/kubernetes/kubernetes/%s/go.mod", version))
	if err != nil {
		return deps, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return deps, err
	}

	f, err := modfile.Parse("go.mod", data, nil)
	if err != nil {
		return deps, err
	}

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

		deps[required.Mod.Path] = required.Mod.Version
	}

	return deps, nil
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

func updateRenovate() cli.Command {
	return cli.Command{
		Name:  "update-renovate",
		Usage: "Update the renovate config with pinned dependencies from k8s upstream",
		Action: func(ctx *cli.Context) error {
			k8sVersion := ctx.String("k8s-version")
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

			for pkg, ver := range k8s {
				_, isIgnored := ignored[pkg]
				if isIgnored {
					continue
				}

				log.Printf(`Pinning %q to %q\n`, pkg, ver)
				rule := packageRule{
					MatchPackageNames: []string{pkg},
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
				Name:     "k8s-version",
				Usage:    "The k8s version to look for",
				Required: true,
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
		Name:  "check",
		Usage: "Check that dependencies from upstream k8s are pinned to the correct version",
		Action: func(ctx *cli.Context) error {
			k8sVersion := ctx.String("k8s-version")
			local, err := localDependencies()
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

			hasError := false

			for kpkg, kver := range k8s {
				lver, exists := local[kpkg]
				if !exists {
					continue
				}

				_, isIgnored := ignored[kpkg]
				if isIgnored {
					continue
				}

				if kver != lver {
					log.Printf("Package %q is different, local=%s vs upstream=%s\n", kpkg, lver, kver)
					hasError = true
				}
			}

			if hasError {
				return fmt.Errorf("some dependencies are not pinned to k8s upstream's version")
			}

			return nil
		},
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:     "k8s-version",
				Usage:    "The k8s version to look for",
				Required: true,
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
