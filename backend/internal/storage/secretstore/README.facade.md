# Portable provider-secret facade — CH-30

`New(channel, explicitBackend)` supplies the typed Go equivalent of Python's
`secret_store.SecretStore`. It never selects or constructs a native backend.
Nil and typed-nil backends fail explicitly. Native policy and platform selection
remain separate from this facade and do not activate it in the product.

Targets preserve `CareerHub/<channel>/provider/<name>` exactly. Names are ASCII
1–80 characters, start with a letter or number, and otherwise allow letters,
numbers, dot, underscore and hyphen. No trimming, case folding or enumeration
is added. Windows may resolve targets case-insensitively at its native layer;
the injected memory backend does not pretend to reproduce that OS behavior.

`Set` validates the name before the value. A value must be nonempty valid UTF-8
and at most 512 encoded bytes. Whitespace, BOM and embedded NUL are retained.
`Get` distinguishes absent from a stored empty value, validates UTF-8, and
deliberately has no facade 512-byte read limit: this matches the Python facade.
Native backends may have their own blob limits. Returned strings do not alias
backend buffers. `Delete` returns the backend's exact found/deleted boolean.

Validation uses safe fixed categories; `ValidationError.Error/Format` never
prints inputs or even a forged field value. `BackendOperationError` records the
fixed attempted operation, formats safely, and unwraps the original cause so
trusted callers retain sentinel identity. Do not log an unwrapped arbitrary
backend error. No retry, regeneration, normalization, alternate credential
target, environment variable or plaintext fallback is present.

Typed-boundary differences: Python nonstring/dynamic values and falsey backend
objects are excluded. A Go string containing malformed UTF-8 is rejected with
`SECRET_VALUE_INVALID_UTF8`; Python's corresponding unpaired-surrogate encoding
failure is not claimed as a same-class source error. Nil/zero-value Go stores
fail safely with backend-required rather than panic. No native-key ownership,
installed-host security, cross-account recovery or data migration is proven here.

Run focused tests/vet with the pinned Go toolchain and offline module settings.
The historical Python reference comparison was removed on 2026-10-15 with the
retired Python runtime.
