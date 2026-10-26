package company

import (
	"database/sql"
	"log"
)

// EnsureEntityTypeSeed seeds the six real production entity-type values
// (given during the Settings-page discussion — see plan §3) when the table
// is empty, same self-migration shape as staff.EnsurePositionRoles.
func EnsureEntityTypeSeed(database *sql.DB) error {
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM EntityType`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	names := []string{"Client", "Contractor", "Consultant", "ManPowerSupply", "Supplier", "Government"}
	for _, name := range names {
		if _, err := database.Exec(`INSERT INTO EntityType (Name) VALUES (?)`, name); err != nil {
			return err
		}
	}
	return nil
}

// EnsureProjectSeed seeds the two real production project names — the same
// "LNG" and "GOKPL" values already used as CompanyRelation.Project in
// seedCompanies below — so the Project dropdown has real options instead of
// starting empty. Same self-migration shape as EnsureEntityTypeSeed.
func EnsureProjectSeed(database *sql.DB) error {
	var n int
	if err := database.QueryRow(`SELECT COUNT(*) FROM ProjectTable`).Scan(&n); err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	names := []string{"LNG", "GOKPL"}
	for _, name := range names {
		if _, err := database.Exec(`INSERT INTO ProjectTable (Name) VALUES (?)`, name); err != nil {
			return err
		}
	}
	return nil
}

type seedCompany struct {
	name         string
	abbreviation string
	entityType   string // matched case-insensitively against EntityType.Name
}

// seedCompanies is the real production company roster, provided directly by
// the user (54 companies across the LNG and GOKPL projects). A few entries
// share the "GOV" abbreviation (several distinct government offices) —
// Company.Abbreviation has no UNIQUE constraint, only Name does, so that's
// fine. entityType is matched case-insensitively against EntityType.Name
// below since the source data used "ManpowerSupply" (lowercase p) while the
// seeded EntityType value is "ManPowerSupply".
var seedCompanies = []seedCompany{
	{"Arisafu Pulo Asap", "APA", "Contractor"},
	{"BMT Asia Indonesia", "BMT", "Consultant"},
	{"Cahaya Lambang Logistik", "CLL", "Contractor"},
	{"China Communication Construction", "CCC", "Contractor"},
	{"China Eleventh Chemical Construction Company Limited", "ELECO", "Contractor"},
	{"China Hebei Construction and Geotechnical Investigation", "CHCI", "Contractor"},
	{"Citec Engineering Indonesia", "CEI", "Contractor"},
	{"CNPC Offshore Engineering Company", "COEC", "Contractor"},
	{"ERM Indonesia", "ERM", "Contractor"},
	{"Gap Logistic", "GAP", "Contractor"},
	{"Hatfield Indonesia", "HAT", "Contractor"},
	{"Kerui", "KERUI", "Contractor"},
	{"KJPP Dirno Farid & Rekan", "KJPP", "Consultant"},
	{"KSO IBT-KPN", "IBTKPN", "ManPowerSupply"},
	{"Kualitas Utama Sertifikasi", "KUS", "Consultant"},
	{"Layar Nusantara Gas", "LNG", "Client"},
	{"Maximos Global", "MAX", "Contractor"},
	{"Momen Construction Energy", "MCE", "Contractor"},
	{"NES Global Teknik", "NES", "Contractor"},
	{"Partono Fondas Engineering Consultant", "PCG", "Contractor"},
	{"Pelayaran Nasional Ekalya Purnamasari", "PNEP", "Contractor"},
	{"Sahabat 95", "S95", "Contractor"},
	{"Salawati Motorindo", "SALA", "Contractor"},
	{"Samudera Energy Tangguh", "SET", "Contractor"},
	{"Samudera Laut Logistic", "SLL", "Contractor"},
	{"Sinar Prapanca", "SP", "Contractor"},
	{"Sinotrans CSC", "SINO", "Contractor"},
	{"Starway Engineering Indonesia", "SEI", "Contractor"},
	{"Technip Energies", "TEN", "Consultant"},
	{"Tectona Mitra Utama", "TMU", "Contractor"},
	{"Tricar", "TRIC", "Contractor"},
	{"Zhongshi Chemical Engineering Construction", "ZSCEC", "Contractor"},
	{"Wastec International", "WI", "Contractor"},
	{"Asih Eka Abadi", "AEA", "Contractor"},
	{"Hubei Geological Engineering Indonesia", "HGEI", "Contractor"},
	{"High Tech Grand Indo", "HTGI", "Contractor"},
	{"Nanggala Dua Delapan", "N28", "Contractor"},
	{"Intraco Dharma Ekatama", "IDE", "Contractor"},
	{"Kantor Imigrasi Manokwari", "GOV", "Government"},
	{"Kementerian Lingkungan Hidup - Badan Pengendalian Lingkungan Hidup", "GOV", "Government"},
	{"Dinas Pendapatan Daerah Kabupaten Bintuni", "GOV", "Government"},
	{"Power Construction Corporation of China", "POWERCHINA", "Contractor"},
	{"Yilybang Konstruksi Indonesia", "YILKI", "Contractor"},
	{"KSO Sikars Petroleum Indonesia", "SIKARS", "Contractor"},
	{"Zoomlion Indonesia Heavy Industry", "ZIHI", "Contractor"},
	{"Dinas Ketenagakerjaan", "GOV", "Government"},
	{"YSK Kontruksi Indonesia", "YSK", "ManPowerSupply"},
	{"Artama Sentosa Indonesia", "ASI", "Contractor"},
	{"KUPP Kelas II Bintuni", "GOV", "Government"},
	{"Arnoldy Sigap Wibawa", "ASW", "Contractor"},
	{"Semesta Nusantara Indonesia", "SEMESTA", "Contractor"},
	{"Anugrah Bendera Cemerlang", "ANUGRAH", "Contractor"},
	{"Genting Oil Kasuri Pte Ltd", "GOKPL", "Client"},
	{"Indocater", "Indocater", "Contractor"},
}

// EnsureCompanySeed seeds the real production Company roster (see
// seedCompanies), inserting only whichever named companies don't already
// exist — per-row idempotent rather than gated on "table is empty", since a
// dev/demo DB can already hold unrelated Company rows (e.g. a manually
// created test company) without that meaning this real roster was ever
// loaded. Requires EntityType to already be seeded (main.go calls
// EnsureEntityTypeSeed first).
func EnsureCompanySeed(database *sql.DB) error {
	for _, c := range seedCompanies {
		var exists int
		if err := database.QueryRow(`SELECT 1 FROM Company WHERE Name = ?`, c.name).Scan(&exists); err == nil {
			continue
		}
		var entityTypeID int64
		if err := database.QueryRow(`SELECT EntityTypeID FROM EntityType WHERE LOWER(Name) = LOWER(?)`, c.entityType).Scan(&entityTypeID); err != nil {
			return err
		}
		if _, err := database.Exec(
			`INSERT INTO Company (Name, Abbreviation, FK_EntityTypeID) VALUES (?, ?, ?)`,
			c.name, c.abbreviation, entityTypeID,
		); err != nil {
			return err
		}
	}
	return nil
}

type seedCompanyRelation struct {
	company string
	parent  string
	project string
	depth   int
}

// seedCompanyRelations is the real production relation chain (48 rows) tying
// each subcontractor back to its client project, provided by the user
// alongside seedCompanies. Two projects: LNG (client Layar Nusantara Gas)
// and GOKPL (client Genting Oil Kasuri Pte Ltd) — clients themselves have no
// relation row (they're the root, Depth 0 implicitly). Salawati Motorindo
// appears twice (once per project) since it subcontracts under both.
var seedCompanyRelations = []seedCompanyRelation{
	{"Anugrah Bendera Cemerlang", "China Eleventh Chemical Construction Company Limited", "LNG", 3},
	{"Arisafu Pulo Asap", "Zhongshi Chemical Engineering Construction", "LNG", 3},
	{"Arnoldy Sigap Wibawa", "Power Construction Corporation of China", "LNG", 3},
	{"Artama Sentosa Indonesia", "Starway Engineering Indonesia", "LNG", 2},
	{"Asih Eka Abadi", "Starway Engineering Indonesia", "LNG", 2},
	{"BMT Asia Indonesia", "Layar Nusantara Gas", "LNG", 1},
	{"Cahaya Lambang Logistik", "Starway Engineering Indonesia", "LNG", 2},
	{"China Communication Construction", "Starway Engineering Indonesia", "LNG", 2},
	{"China Eleventh Chemical Construction Company Limited", "Starway Engineering Indonesia", "LNG", 2},
	{"China Hebei Construction and Geotechnical Investigation", "Starway Engineering Indonesia", "LNG", 2},
	{"Citec Engineering Indonesia", "Starway Engineering Indonesia", "LNG", 2},
	{"CNPC Offshore Engineering Company", "Starway Engineering Indonesia", "LNG", 2},
	{"ERM Indonesia", "Starway Engineering Indonesia", "LNG", 2},
	{"Gap Logistic", "Starway Engineering Indonesia", "LNG", 2},
	{"Hatfield Indonesia", "Starway Engineering Indonesia", "LNG", 2},
	{"High Tech Grand Indo", "Starway Engineering Indonesia", "LNG", 2},
	{"Hubei Geological Engineering Indonesia", "China Eleventh Chemical Construction Company Limited", "LNG", 3},
	{"Intraco Dharma Ekatama", "Power Construction Corporation of China", "LNG", 3},
	{"Kerui", "Layar Nusantara Gas", "LNG", 1},
	{"KJPP Dirno Farid & Rekan", "Layar Nusantara Gas", "LNG", 1},
	{"KSO IBT-KPN", "Layar Nusantara Gas", "LNG", 1},
	{"KSO Sikars Petroleum Indonesia", "Genting Oil Kasuri Pte Ltd", "GOKPL", 1},
	{"Kualitas Utama Sertifikasi", "Starway Engineering Indonesia", "LNG", 2},
	{"Maximos Global", "Starway Engineering Indonesia", "LNG", 2},
	{"Momen Construction Energy", "Starway Engineering Indonesia", "LNG", 2},
	{"Nanggala Dua Delapan", "Starway Engineering Indonesia", "LNG", 2},
	{"NES Global Teknik", "Starway Engineering Indonesia", "LNG", 2},
	{"Partono Fondas Engineering Consultant", "Starway Engineering Indonesia", "LNG", 2},
	{"Pelayaran Nasional Ekalya Purnamasari", "Layar Nusantara Gas", "LNG", 1},
	{"Power Construction Corporation of China", "Starway Engineering Indonesia", "LNG", 2},
	{"Sahabat 95", "Layar Nusantara Gas", "LNG", 1},
	{"Salawati Motorindo", "Starway Engineering Indonesia", "LNG", 2},
	{"Samudera Energy Tangguh", "Starway Engineering Indonesia", "LNG", 3},
	{"Samudera Laut Logistic", "Starway Engineering Indonesia", "LNG", 2},
	{"Semesta Nusantara Indonesia", "China Eleventh Chemical Construction Company Limited", "LNG", 3},
	{"Sinar Prapanca", "Starway Engineering Indonesia", "LNG", 2},
	{"Sinotrans CSC", "Starway Engineering Indonesia", "LNG", 2},
	{"Starway Engineering Indonesia", "Layar Nusantara Gas", "LNG", 1},
	{"Technip Energies", "Layar Nusantara Gas", "LNG", 1},
	{"Tectona Mitra Utama", "Starway Engineering Indonesia", "LNG", 2},
	{"Tricar", "Starway Engineering Indonesia", "LNG", 2},
	{"Wastec International", "Starway Engineering Indonesia", "LNG", 2},
	{"Yilybang Konstruksi Indonesia", "Power Construction Corporation of China", "LNG", 3},
	{"YSK Kontruksi Indonesia", "China Eleventh Chemical Construction Company Limited", "LNG", 3},
	{"Zhongshi Chemical Engineering Construction", "Starway Engineering Indonesia", "LNG", 2},
	{"Zoomlion Indonesia Heavy Industry", "Power Construction Corporation of China", "LNG", 3},
	{"Salawati Motorindo", "KSO Sikars Petroleum Indonesia", "GOKPL", 2},
	{"Indocater", "Starway Engineering Indonesia", "LNG", 2},
}

// EnsureCompanyRelationSeed seeds the real production relation chain (see
// seedCompanyRelations), inserting only whichever relations don't already
// exist by RelationCode — same per-row idempotency as EnsureCompanySeed,
// for the same reason (don't gate on "table is empty"). Requires Company to
// already be seeded (main.go calls EnsureCompanySeed first); if a company
// or parent name can't be resolved (a real data mismatch, not expected in
// normal operation), that one relation is skipped with a logged warning
// rather than failing the whole server startup — a bad seed row shouldn't
// block the app from starting.
func EnsureCompanyRelationSeed(database *sql.DB) error {
	for _, r := range seedCompanyRelations {
		var companyID int64
		var abbreviation string
		if err := database.QueryRow(`SELECT CompanyID, COALESCE(Abbreviation,'') FROM Company WHERE Name = ?`, r.company).Scan(&companyID, &abbreviation); err != nil {
			log.Printf("company seed: skipping relation for %q — company not found: %v", r.company, err)
			continue
		}
		var parentID int64
		if err := database.QueryRow(`SELECT CompanyID FROM Company WHERE Name = ?`, r.parent).Scan(&parentID); err != nil {
			log.Printf("company seed: skipping relation for %q — parent %q not found: %v", r.company, r.parent, err)
			continue
		}
		relationCode := abbreviation + "-" + r.project

		var exists int
		if err := database.QueryRow(`SELECT 1 FROM CompanyRelation WHERE RelationCode = ?`, relationCode).Scan(&exists); err == nil {
			continue
		}
		if _, err := database.Exec(
			`INSERT INTO CompanyRelation (RelationCode, FK_CompanyID, FK_ParentID, Project, Depth) VALUES (?, ?, ?, ?, ?)`,
			relationCode, companyID, parentID, r.project, r.depth,
		); err != nil {
			return err
		}
	}
	return nil
}
