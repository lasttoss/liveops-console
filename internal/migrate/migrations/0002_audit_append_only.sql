-- An audit trail that can be edited is a log, and logs are not evidence. The trigger
-- turns "we promise not to update this" into a database error, and the test in
-- internal/pgstore asserts the error is real rather than trusting the wording here.
CREATE OR REPLACE FUNCTION audit_log_is_append_only() RETURNS trigger AS $fn$
BEGIN
  RAISE EXCEPTION 'audit_log is append-only: % rejected (row id %)', TG_OP, COALESCE(OLD.id, NEW.id);
END;
$fn$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS audit_log_no_update ON audit_log;
CREATE TRIGGER audit_log_no_update
  BEFORE UPDATE OR DELETE ON audit_log
  FOR EACH ROW EXECUTE FUNCTION audit_log_is_append_only();

DROP TRIGGER IF EXISTS audit_log_no_truncate ON audit_log;
CREATE TRIGGER audit_log_no_truncate
  BEFORE TRUNCATE ON audit_log
  FOR EACH STATEMENT EXECUTE FUNCTION audit_log_is_append_only();
