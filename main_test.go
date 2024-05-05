package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	os.Exit(testscript.RunMain(m, map[string]func() int{
		"kubemodcmp": func() (exitCode int) {
			defer func() {
				if val := recover(); val != nil {
					exitCode = recover().(int)
				}
			}()
			main()
			return 0
		},
	}))
}

func TestScript(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:      "testdata",
		TestWork: true,
		Setup: func(env *testscript.Env) error {
			path := os.Getenv("GOPATH")
			if os.Getenv("GOPATH") == "" {
				path = filepath.Join(env.WorkDir, ".go")
			}
			env.Setenv("GOPATH", path)

			if os.Getenv("GOCACHE") != "" {
				env.Setenv("GOCACHE", os.Getenv("GOCACHE"))
			} else {
				env.Setenv("GOCACHE", filepath.Join(path, "cache"))
			}

			// TODO: Use global cache instead of one per txtar
			if os.Getenv("GOMODCACHE") != "" {
				env.Setenv("GOMODCACHE", os.Getenv("GOMODCACHE"))
			} else {
				env.Setenv("GOMODCACHE", filepath.Join(path, "modcache"))
			}
			return nil
		},
	})
}
