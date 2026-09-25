-- Уведомления о событии (см. artifacts/PET/pages/calendar/dobavlenie-sobytiya-backend.md,
-- redaktirovanie-sobytiya-backend.md, prosmotr-kalendarya-backend.md):
-- признак того, что для события включены клиентские локальные уведомления.
-- Сервер только хранит флаг, не участвует в доставке самого уведомления.
ALTER TABLE event
  ADD COLUMN notifications_enabled boolean NOT NULL DEFAULT false;
