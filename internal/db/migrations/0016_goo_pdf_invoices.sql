-- Goocommerce › PDF Invoices & Packing Slips: document number counters, incremented atomically.
-- applied is the "Next invoice number" setting last applied, so changing the setting resets the counter.
CREATE TABLE IF NOT EXISTS goo_pdf_counters (
	name    TEXT PRIMARY KEY,
	next    BIGINT NOT NULL DEFAULT 1,
	applied BIGINT NOT NULL DEFAULT 0
);
