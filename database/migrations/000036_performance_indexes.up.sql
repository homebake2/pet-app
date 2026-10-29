-- Индексы для запросов, которые без них перебирают таблицу целиком.

-- Удаление файлов владельцев (DeleteFilesByOwnersWith) удаляет и
-- неподтверждённые строки, а частичный индекс по подтверждённым к такому
-- условию не подходит. Полный индекс обслуживает и чтение подтверждённых.
DROP INDEX IF EXISTS file_owner_confirmed_idx;
CREATE INDEX file_owner_idx ON file (owner_type, owner_id);

-- Проверка «на объект S3 не осталось ссылок» после удаления файлов
-- (unreferencedObjectKeysWith).
CREATE INDEX file_object_key_idx ON file (object_key);

-- Внешние ключи ON DELETE SET NULL: при удалении настроек напоминания и
-- события Postgres ищет ссылающиеся строки по этим столбцам.
CREATE INDEX medication_reminder_plan_id_idx ON medication (reminder_plan_id) WHERE reminder_plan_id IS NOT NULL;
CREATE INDEX vaccination_next_plan_id_idx ON vaccination (next_plan_id) WHERE next_plan_id IS NOT NULL;
CREATE INDEX vaccination_administered_event_id_idx ON vaccination (administered_event_id) WHERE administered_event_id IS NOT NULL;
