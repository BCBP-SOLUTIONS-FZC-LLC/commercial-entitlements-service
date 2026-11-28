-- PostgreSQL cannot drop an enum value. Rolling back requires that no row
-- uses 'forfeited'; the value itself is left in place (harmless when unused).
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM quota_addons WHERE status = 'forfeited') THEN
    RAISE EXCEPTION 'cannot roll back 000007: quota_addons rows with status forfeited exist';
  END IF;
END
$$;
