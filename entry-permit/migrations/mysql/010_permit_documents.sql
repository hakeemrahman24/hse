-- 010 permit documents (MariaDB). Keep in step with sqlite/010_permit_documents.sql. Run by hand after a mysqldump.
-- Days are CHAR(10), file columns hold a path relative to the upload folder.
ALTER TABLE permit ADD COLUMN contract_until CHAR(10) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN contract_doc VARCHAR(80) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN bpjs_kes VARCHAR(30) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN bpjs_kes_doc VARCHAR(80) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN bpjs_tk VARCHAR(30) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN bpjs_tk_doc VARCHAR(80) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN cert_until CHAR(10) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN cert_doc VARCHAR(80) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN cert_name VARCHAR(200) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN position_canonical VARCHAR(120) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN position_risk VARCHAR(20) NOT NULL DEFAULT '';
ALTER TABLE permit ADD COLUMN cert_required VARCHAR(4) NOT NULL DEFAULT '';
