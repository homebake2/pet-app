-- created_at для стабильной сортировки списка питомцев (GET /pet ORDER BY created_at).
ALTER TABLE pet ADD COLUMN created_at timestamptz NOT NULL DEFAULT now();
