ALTER TABLE admin_users ADD COLUMN IF NOT EXISTS must_change boolean NOT NULL DEFAULT false;
