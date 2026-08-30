-- Питомцы принадлежат пользователю напрямую, без обращения к таблице profile
-- (которая создаётся только явно через POST /profile и может отсутствовать).

ALTER TABLE pet ADD COLUMN user_id uuid REFERENCES users(id);
UPDATE pet SET user_id = profile.user_id FROM profile WHERE profile.id = pet.profile_id;
ALTER TABLE pet ALTER COLUMN user_id SET NOT NULL;
ALTER TABLE pet DROP COLUMN profile_id;
CREATE INDEX IF NOT EXISTS idx_pet_user_id ON pet(user_id);
