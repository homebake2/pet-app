-- Вакцинации: время суток следующей вакцинации и момент завершения.
-- next_time задано, только когда задан next_date; completed_at записывается
-- отметкой «выполнено» напоминания на следующую вакцинацию (при заданном
-- completed_at next_date IS NULL). Дополнительные напоминания хранятся как
-- обычные настройки reminder_plan (source = 'vaccination'), отдельной
-- таблицы нет.
ALTER TABLE vaccination
    ADD COLUMN next_time     time        NULL,
    ADD COLUMN completed_at  timestamptz NULL;
