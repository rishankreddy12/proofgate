-- Make admin_audit tamper-evident and strictly append-only
CREATE OR REPLACE FUNCTION admin_audit_reject_modification()
RETURNS TRIGGER AS $$
BEGIN
    RAISE EXCEPTION 'admin_audit is append-only: UPDATE and DELETE operations are prohibited';
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_admin_audit_append_only ON admin_audit;
CREATE TRIGGER trg_admin_audit_append_only
BEFORE UPDATE OR DELETE ON admin_audit
FOR EACH ROW
EXECUTE FUNCTION admin_audit_reject_modification();
