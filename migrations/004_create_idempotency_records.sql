CREATE TABLE idempotency_records (
    operation TEXT NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    request_fingerprint BYTEA NOT NULL
        CHECK (octet_length(request_fingerprint) = 32),
    response_status SMALLINT,
    response_body JSONB,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    completed_at TIMESTAMPTZ,
    PRIMARY KEY (operation, idempotency_key),
    CHECK (
        (
            response_status IS NULL
            AND response_body IS NULL
            AND completed_at IS NULL
        )
        OR
        (
            response_status BETWEEN 100 AND 599
            AND response_body IS NOT NULL
            AND completed_at IS NOT NULL
        )
    )
);
