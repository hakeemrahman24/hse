// The clinic's calendar day (Asia/Jayapura, UTC+9) as YYYY-MM-DD, whatever
// timezone this browser is set to. Keep the zone in step with
// dates.ClinicLocation in internal/dates/dates.go. Business dates are plain
// YYYY-MM-DD text, so compare them as text.
const CLINIC_TIME_ZONE = "Asia/Jayapura";
const clinicDateFormat = new Intl.DateTimeFormat("en-CA", {
  timeZone: CLINIC_TIME_ZONE,
  year: "numeric",
  month: "2-digit",
  day: "2-digit",
});

function clinicToday() {
  return clinicDateFormat.format(new Date());
}

// Whole calendar days from `fromIso` to `toIso` (positive when `toIso` is
// later), or null when either is not a real YYYY-MM-DD date.
function clinicDaysBetween(fromIso, toIso) {
  const parse = (iso) => {
    const m = /^(\d{4})-(\d{2})-(\d{2})$/.exec(String(iso || "").trim());
    if (!m) return null;
    const ms = Date.UTC(Number(m[1]), Number(m[2]) - 1, Number(m[3]));
    const d = new Date(ms);
    const real = d.getUTCFullYear() === Number(m[1]) && d.getUTCMonth() === Number(m[2]) - 1 && d.getUTCDate() === Number(m[3]);
    return real ? ms : null;
  };
  const from = parse(fromIso);
  const to = parse(toIso);
  return from === null || to === null ? null : Math.round((to - from) / 86400000);
}
