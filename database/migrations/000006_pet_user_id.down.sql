ALTER TABLE pet ADD COLUMN profile_id uuid REFERENCES profile(id) ON DELETE CASCADE;
UPDATE pet SET profile_id = profile.id FROM profile WHERE profile.user_id = pet.user_id;
ALTER TABLE pet ALTER COLUMN profile_id SET NOT NULL;
ALTER TABLE pet DROP COLUMN user_id;
CREATE INDEX IF NOT EXISTS idx_pet_profile_id ON pet(profile_id);
