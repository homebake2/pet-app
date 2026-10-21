ALTER TABLE medication  DROP COLUMN user_id;
ALTER TABLE allergy     DROP COLUMN user_id;
ALTER TABLE vet_visit   DROP COLUMN user_id;
ALTER TABLE disease     DROP COLUMN user_id;
ALTER TABLE vaccination DROP COLUMN user_id;

DROP TABLE IF EXISTS pet_access_notice;
DROP TABLE IF EXISTS pet_access_invite_pet;
DROP TABLE IF EXISTS pet_access_invite;
DROP TABLE IF EXISTS pet_access;
