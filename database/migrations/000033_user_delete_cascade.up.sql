-- Удаление аккаунта (DELETE /profile) каскадно удаляет питомцев, события и
-- настройки напоминаний пользователя; дальше каскад идёт по связям этих таблиц.
ALTER TABLE pet DROP CONSTRAINT pet_user_id_fkey;
ALTER TABLE pet ADD CONSTRAINT pet_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE event DROP CONSTRAINT event_user_id_fkey;
ALTER TABLE event ADD CONSTRAINT event_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;

ALTER TABLE reminder_plan DROP CONSTRAINT reminder_plan_user_id_fkey;
ALTER TABLE reminder_plan ADD CONSTRAINT reminder_plan_user_id_fkey
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
