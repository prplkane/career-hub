# Code-owned document resources

`theme1.xml` is the unmodified theme member of `docx/templates/default.docx`
from python-docx 1.2.0 (MIT); `LICENSE.python-docx` carries the upstream
notice. It contains font names, not font binaries. The DOCX renderer embeds it
so its default styles keep the same script-specific font fallbacks.

`Vera.ttf`, `VeraBd.ttf` and `bitstream-vera-license.txt` are unmodified
Bitstream Vera resources. Their SHA-256 values are:

```text
Vera.ttf  c4c45690b345435b2cba52ecabe275f05e49b389b39fe68ad03afbb551288d3d
VeraBd.ttf  cc037385e4d55bfde89b13e03091ee93bf40c0c52ddd391ff031ab276f13b8e9
bitstream-vera-license.txt  3361d054759a2fc686a2c058be82deaf9c2e6fe549be9004d7935a6c1736315d
```

The PDF writer embeds subsets of these two faces. Missing characters fail
explicitly; no host or remote font is substituted.

Runtime dependency pins and checksums are in `backend/go.mod` and `go.sum`:

- gopdf v0.38.1, MIT (`LICENSE.gopdf`).
- ledongthuc/pdf 6c8c28e0e8a0, BSD-3-Clause (`LICENSE.pdf-reader`).
- gopdf's gofpdi 1f10f9844311 dependency, MIT (`LICENSE.gofpdi`); its PDF
  importer is never invoked by Career Hub's renderer.
- gofpdi's pkg/errors v0.8.1 dependency, BSD-2-Clause (`LICENSE.pkg-errors`).

All these notices and the full Vera notice must accompany the packaged
runtime.
