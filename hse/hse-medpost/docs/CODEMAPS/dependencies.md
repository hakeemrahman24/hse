<!-- Generated: 2026-10-20 | Files scanned: go.mod, systemd, indonesia-region | Token estimate: ~350 -->
# Dependencies

## Go modules
modernc.org/sqlite (dev DB, pure Go) · go-sql-driver/mysql (prod) · golang.org/x/crypto (passwords)
excelize/v2 (xlsx export) · go-pdf/fpdf (certificates, reports) · digitorus/pdfsign + pdf + go-pkcs12 (certificate signing) · skip2/go-qrcode (certificate QR)

## External services
MariaDB on the Pi (prod) · SMTP (MCU expiry email — shelved) · Google Sheet export-back / entry-permit IMPORTRANGE (designed, not built)
Region data: GitHub `cahyadsn` wilayah dataset, pinned commit d68e8d55, via `indonesia-region/update.py` (validated, never executes downloaded SQL)

## Front end
No framework, no bundler, no CDN. Fonts/CSS self-hosted in web/static/clearwave.
