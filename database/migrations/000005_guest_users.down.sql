DROP INDEX IF EXISTS idx_users_guest_device_id;
ALTER TABLE users DROP COLUMN is_guest;
ALTER TABLE users DROP COLUMN guest_device_id;
