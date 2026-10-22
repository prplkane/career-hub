# go-yaml dependency notices

Dependency: `go.yaml.in/yaml/v4 v4.0.0-rc.6`.
Upstream: <https://github.com/yaml/go-yaml/tree/v4.0.0-rc.6>.

`LICENSE` and `NOTICE` retain the upstream module's notices (line endings may
be normalized by Git). `MIT-LICENSE.libyaml.txt` supplies the permission text
linked by upstream NOTICE for the MIT-covered libyaml portions. The dependency
is **not Apache-only**: preserve all three files in any future distribution.
Upstream's NOTICE identifies the covered file list and attribution; some v4
files have subsequently been reorganized. This directory adds no vendored code.

This is a prerelease dependency of an offline migration adapter, not approval
to add it to the production sidecar or an application installer. Packaging and
release review must confirm notice inclusion at the actual cutover.
