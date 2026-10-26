-- Audit finding: Patient.Name has no index (unlike NationalityTable/Company/
-- CheckUpCriteriaTable/TaskCategoryTable, which are all UNIQUE and so get an
-- implicit one). Not UNIQUE here — two different real people can share a
-- name, DOB is the actual disambiguator per the plan's dedupe design — but
-- every name lookup (ListPatients' ?q= search, mcu.js's typeahead, and the
-- new bulk importer's find-or-create-by-Name+DOB match) was a full table
-- scan without this.
CREATE INDEX IF NOT EXISTS idx_patient_name ON Patient(Name);
