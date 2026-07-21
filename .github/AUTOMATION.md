# Alpha automation

The automation configuration intentionally lives on the default `main` branch.
GitHub only runs scheduled workflows from the default branch, while the Go
project being maintained lives on `Alpha`.

## Upstream synchronization

`sync-alpha.yml` checks `MetaCubeX/mihomo:Alpha` every six hours. When upstream
has commits that are not in the fork, it reads the supported Go versions from
upstream's own test matrix, builds a candidate merge, and runs the normal and
`with_gvisor` Go test suites with every listed Go version. The workflow updates
the fork's `Alpha` branch only after every test job succeeds.

The workflow uses a regular (non-force) push. If someone updates the fork while
the test matrix is running, the push safely fails and the next scheduled run
retries from the new branch state. Merge conflicts also stop the update for
manual resolution instead of overwriting fork-only work.

## Component updates

Dependabot checks Go modules and GitHub Actions weekly. Go module updates are
grouped into MetaCubeX components, Go ecosystem components, and other
components. Pull requests target `Alpha`, so its existing test and build
workflows validate dependency changes before they are merged.

GitHub Actions used by the default branch are tracked separately so the sync
controller itself stays current.
