# Kube Mod Cmp

KubeModCmp is a program that helps enforce specific upstream k8s versioning in
projects using k8s libraries.

For example, k8s v1.28.6 [depends on github.com/prometheus/client_golang
v1.16.0]. If your project depends on a version of
github.com/prometheus/client_golang other than v1.16.0, KubeModCmp will display
the difference and exit with an error.

```
$ kubemodcmp check path/to/repo
2024/04/24 17:22:02 Package "github.com/prometheus/client_golang" is different, local=v1.19.0 vs upstream=v1.16.0
2024/04/24 17:22:02 some dependencies are not pinned to k8s upstream's version
```

[depends on github.com/prometheus/client_golang v1.16.0]: https://github.com/kubernetes/kubernetes/blob/v1.28.6/go.mod#L60

## ✨ Features

- 🔒 Enforce versions of dependencies in `go.mod` shared with kubernetes
- 🐹 Enforce minimum Go version that is supported by kubernetes
- ⚙️  Optionally ignore dependencies for more flexibility (eg: test frameworks)
- 🔍 Auto-detect upstream k8s version
- 🤖 Dynamically adjust renovate config to prevent PRs updating dependencies to
  unsupported versions
- 🚀 Github Action to easily enforce in your CI
- 🔨 Auto-fix differences with the `--fix` flag

## Github Action

We support running KubeModCmp as a github action.

### Configuration

Here are the current inputs supported by the Github Action.

**action**

Decides which action to run.

- `check` will run `kubemodcmp check`

**ignore_file**

Specifies the `--ignore-file` argument

**k8s_version**

Specifies the `--k8s_version` argument

### Running check

The following snippets shows an example of running the `kubemodcmp check`
command as a GHA step.

```yaml
on: [push]

jobs:
  test:
    runs-on: ubuntu-latest
    name: Test
    steps:
      - uses: actions/checkout@v4
      - name: Compare kubernetes upstream go.mod
        uses: tomleb/kube-mod-cmp@v0.2
        with:
          action: 'check'
```

### Modifying renovate

TODO
