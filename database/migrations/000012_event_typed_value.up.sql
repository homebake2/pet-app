-- Типизированное значение события: event.value становится jsonb-объектом,
-- размеченным по event.type (см. «Модель значения события и реестр метрик»).
--
-- Миграция выполняет чистую замену структуры, а не конвертацию: старое
-- значение — плоская строка, смысл которой зависел от типа события, и
-- корректного автоматического преобразования её в объект новой формы не
-- существует ни для одного типа, кроме weight. Частичная конвертация
-- «только вес, остальное потерять» оставила бы БД в состоянии, где часть
-- событий валидна, а часть — молча пуста. Обратная совместимость не
-- требуется (PET-1).

-- 1. Удалить накопленные события. Шаг обязателен и выполняется ДО изменения
--    структуры: ADD COLUMN value jsonb NOT NULL упал бы на существующих
--    строках. Таблица pet и данные пользователей не затрагиваются.
DELETE FROM event;

-- 2-3. Заменить столбец. NOT NULL без DEFAULT безопасен только потому, что
--      таблица очищена: умолчания у типизированного значения нет и быть не
--      должно.
ALTER TABLE event DROP COLUMN value;
ALTER TABLE event ADD COLUMN value jsonb NOT NULL;

-- 4. Снять CHECK-ограничение на event.type, если оно есть: множество
--    допустимых типов проверяется приложением по реестру метрик, а не
--    ограничением БД — реестр меняется чаще, чем допустимо гонять миграции,
--    и правило формы value всё равно невыразимо через CHECK.
DO $$
DECLARE
    constraint_name text;
BEGIN
    FOR constraint_name IN
        SELECT con.conname
        FROM pg_constraint con
        JOIN pg_class rel ON rel.oid = con.conrelid
        JOIN pg_namespace nsp ON nsp.oid = rel.relnamespace
        WHERE rel.relname = 'event'
          AND nsp.nspname = current_schema()
          AND con.contype = 'c'
          AND pg_get_constraintdef(con.oid) LIKE '%type%'
    LOOP
        EXECUTE format('ALTER TABLE event DROP CONSTRAINT %I', constraint_name);
    END LOOP;
END
$$;

-- 5. Индекс под агрегацию событий для графиков (GET /events/stats).
CREATE INDEX event_pet_type_date_idx
  ON event (pet_id, type, date_time)
  WHERE deleted_at IS NULL;
