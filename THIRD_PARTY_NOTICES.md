# Third-party notices

## MyGo

https://github.com/egoist/mygo — pinned to v0.6.4.

The native UI uses `github.com/egoist/mygo` and `github.com/egoist/mygo/ui`.
Its documentation and examples were consulted for the view and lifecycle APIs.

> MIT License
>
> Copyright (c) 2026 MyGo contributors
>
> Permission is hereby granted, free of charge, to any person obtaining a copy
> of this software and associated documentation files (the "Software"), to deal
> in the Software without restriction, including without limitation the rights
> to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
> copies of the Software, and to permit persons to whom the Software is
> furnished to do so, subject to the following conditions:
>
> The above copyright notice and this permission notice shall be included in all
> copies or substantial portions of the Software.
>
> THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
> IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
> FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
> AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
> LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
> OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
> SOFTWARE.

## mihomo

https://github.com/MetaCubeX/mihomo — proxy kernel source on branch `Meta`.

The application downloads the latest stable kernel directly from official
GitHub Releases, verifies the release asset's SHA-256 digest, and invokes it
as a separate process through its local REST API. The kernel is not embedded
in the application binary. The UI links to the official release page, where
the corresponding tagged source archives are available.

The kernel is licensed under GPL-3.0. Anyone redistributing it, including in
an installer or mirror, must preserve its license and satisfy its
corresponding-source obligations. Downloading the official binary does not
change its license. The application's MIT license does not replace the
kernel's license.

## Other Go dependencies

The exact versions and checksums are recorded in `go.mod` and `go.sum`.
Dependency license files are included in their source modules; binary release
packagers must retain the applicable notices for all bundled dependencies.
Release packages include the project's LICENSE, this notice, and a generated
THIRD_PARTY_LICENSES.txt containing the Go toolchain and module license notices.
The release workflow produces unsigned Windows installers and ad-hoc-signed,
non-notarized macOS packages; it does not bundle the mihomo kernel.
