-- +goose Up
-- +goose StatementBegin
SELECT 'up SQL query';
CREATE TABLE users (
    id BIGINT PRIMARY KEY,
    tgid BIGINT NOT NULL,
    apitoken TEXT NOT NULL,
    accountid TEXT NOT NULL
)
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
SELECT 'down SQL query';
-- +goose StatementEnd
