-- PostgreSQL cannot drop an enum value. Rolling back requires that no row
-- uses 'refunded'; the value itself is left in place (harmless when unused).
DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM quota_addons WHERE status = 'refunded') THEN
    RAISE EXCEPTION 'cannot roll back 000006: quota_addons rows with status refunded exist';
  END IF;
END
$$;
