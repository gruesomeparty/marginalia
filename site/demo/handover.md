# Ingest rework — sign-off

## Flow

```mermaid
flowchart LR
  client --> api
  api --> queue
  queue --> worker
  worker -.retry.-> queue
  worker --> store
  worker --> dlq
```

## Behaviour

The batch cap is 500 records. Retries use no backoff. Failures go to the dead
letter queue after three attempts.

## Rollout

- Ship behind a flag, default off.
- Enable for one tenant, watch for a day.
- Enable for everyone.
