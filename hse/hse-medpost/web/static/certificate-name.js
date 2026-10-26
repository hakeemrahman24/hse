// Same rules as certificateOpenFilename in internal/entrymcu/certificate.go.
// CheckUpID-CompanyAbbrev-JohnDoe-yyyymmdd.pdf. The open link ends with
// this name because the browser PDF viewer saves the last part of the address.
function certificateToken(value, keepHyphen) {
  let out = "";
  for (const ch of [...String(value || "").trim()]) {
    if (/^[\p{L}\p{N}]$/u.test(ch) || (keepHyphen && ch === "-")) out += ch;
  }
  if ([...out].length > 60) out = [...out].slice(0, 60).join("");
  return out;
}

function certificateDigits(value) {
  return String(value || "").replace(/\D/g, "").slice(0, 8);
}

function certificateOpenFilename(checkUpId, companyAbbrev, patientName, validUntil) {
  const id = certificateToken(checkUpId, true) || "checkup";
  const abbr = certificateToken(companyAbbrev, false) || "NA";
  const name = certificateToken(patientName, false) || "Unknown";
  const date = certificateDigits(validUntil) || "nodate";
  return `${id}-${abbr}-${name}-${date}.pdf`;
}

function certificateOpenHref(mcuRecordId, checkUpId, companyAbbrev, patientName, validUntil) {
  return `/api/mcu-records/${mcuRecordId}/${encodeURIComponent(certificateOpenFilename(checkUpId, companyAbbrev, patientName, validUntil))}`;
}
