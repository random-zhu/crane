# Crane Cost Platform Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the single-process file-only cost ledger with a publish-on-complete MySQL-backed foundation, durable collection leases, raw-artifact archival, and a two-replica-ready `crane-cost` service while preserving the existing cost APIs and four domestic cloud providers.

**Architecture:** Keep cloud protocol logic in the existing Provider packages, split ingestion publication, job leasing, and raw artifact storage into focused interfaces, and provide file-system development implementations plus MySQL 8 and S3-compatible production implementations. `crane-cost` owns these dependencies; `craned`, Kubernetes allocation, international providers, and optimization scoring remain outside this phase.

**Tech Stack:** Go 1.25, `database/sql`, MySQL 8.0, `github.com/go-sql-driver/mysql`, S3-compatible object storage via `github.com/minio/minio-go/v7`, Prometheus client, Helm 3, Kubernetes 1.34-1.36, GitHub Actions.

## Global Constraints

- Follow the approved design in `docs/superpowers/specs/2026-07-20-crane-multicloud-cost-platform-design.md`.
- Preserve exact financial values as `model.Decimal`; never persist or calculate financial amounts with `float64`.
- Keep `estimated` and `billed` data physically separate; this phase implements billed ingestion only.
- Do not store bill line items in Prometheus or Kubernetes CRDs.
- Do not add Kafka, ClickHouse, OpenCost runtime dependencies, or cloud credentials to `craned`.
- Keep the existing `/healthz`, `/readyz`, `/metrics`, `/api/v1/providers`, `/api/v1/costs`, `/api/v1/costs/summary`, `/api/v1/rates`, and `/api/v1/allocate` routes and successful response fields compatible; `/readyz` adopts the infrastructure-based semantics required by the approved design.
- Production uses MySQL and S3-compatible storage; file storage remains an explicit development backend.
- Production deployment uses two replicas, non-root containers, read-only root filesystems, and the Restricted Pod Security profile.
- Provider failures remain isolated by provider, account, and billing month.
- Every schema change is forward-only and safe while the previous application version is still running.

---

## File Structure

Create or change the following ownership boundaries before implementing behavior:

```text
pkg/cost/ledger/store.go                 published ledger and query contracts
pkg/cost/ledger/run.go                   ingestion run types and state transitions
pkg/cost/ledger/contract/contract.go     reusable backend conformance tests
pkg/cost/ledger/file.go                  development backend, including staged runs
pkg/cost/ledger/mysql/store.go           MySQL published ledger
pkg/cost/ledger/mysql/run.go             MySQL staging and publication
pkg/cost/ledger/mysql/query.go           MySQL filtered queries and summaries
pkg/cost/ledger/mysql/migrate.go         embedded migration runner
pkg/cost/ledger/mysql/migrations/*.sql   forward-only schema
pkg/cost/artifact/store.go               raw artifact contract
pkg/cost/artifact/file/store.go          development artifact backend
pkg/cost/artifact/s3/store.go            S3-compatible production backend
pkg/cost/scheduler/types.go              collection job states and errors
pkg/cost/scheduler/repository.go         lease repository contract
pkg/cost/scheduler/mysql.go              MySQL lease implementation
pkg/cost/scheduler/memory.go             single-process development repository
pkg/cost/scheduler/planner.go            daily current-plus-two-month job creation
pkg/cost/scheduler/worker.go             claim, heartbeat, execute, retry loop
pkg/cost/service/dependencies.go          backend construction and injection
pkg/cost/service/config.go               storage, artifact, and scheduler config
pkg/cost/service/service.go              service lifecycle and compatibility API
cmd/crane-cost/main.go                    canonical binary entry point
cmd/cost-collector/main.go                compatibility entry point
charts/crane-cost-collector/              two-replica production chart
deploy/cost-collector/                    example production manifests
```

---

### Task 1: Define Published-Run Domain Types and Store Contracts

**Files:**
- Modify: `pkg/cost/model/types.go`
- Modify: `pkg/cost/model/types_test.go`
- Modify: `pkg/cost/ledger/store.go`
- Create: `pkg/cost/ledger/run.go`
- Test: `pkg/cost/ledger/run_test.go`

**Interfaces:**
- Consumes: existing `model.CostLineItem`, `model.BillCursor`, `ledger.Query`, and `ledger.UpsertResult`.
- Produces: `ledger.Run`, `ledger.RunStatus`, `ledger.RunQuery`, `ledger.Page`, `ledger.Publication`, `ledger.PublishingStore`, `ledger.QueryStore`, and `ledger.RunReader`.

- [ ] **Step 1: Write failing validation tests for ingestion runs**

```go
func TestRunValidate(t *testing.T) {
    period := model.BillingPeriod{
        Start: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
        End:   time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC),
    }
    valid := Run{
        ID: "run-1", JobID: "run-1", LeaseEpoch: 1,
        Provider: model.ProviderAliyun,
        AccountID: "payer", Period: period, Status: RunPending,
        CreatedAt: time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC),
    }
    if err := valid.Validate(); err != nil {
        t.Fatalf("valid run rejected: %v", err)
    }
    valid.ID = ""
    if err := valid.Validate(); err == nil {
        t.Fatal("run without id was accepted")
    }
}
```

- [ ] **Step 2: Run the focused test and confirm the missing types fail compilation**

Run: `go test ./pkg/cost/ledger -run TestRunValidate -count=1`

Expected: FAIL with undefined `Run` or `RunPending`.

- [ ] **Step 3: Add run types and split the store interfaces**

```go
type RunStatus string

const (
    RunPending   RunStatus = "pending"
    RunRunning   RunStatus = "running"
    RunPublished RunStatus = "published"
    RunFailed    RunStatus = "failed"
)

type Run struct {
    ID         string
    JobID      string
    LeaseEpoch uint64
    Provider   model.Provider
    AccountID  string
    Period     model.BillingPeriod
    Status     RunStatus
    CreatedAt  time.Time
    UpdatedAt  time.Time
    PublishedAt time.Time
}

type Page struct {
    Number     int
    Items      []model.CostLineItem
    Artifact   *ArtifactRef
    NextCursor *model.BillCursor
    Complete   bool
}

type ArtifactRef struct {
    Key           string
    ETag          string
    SHA256        string
    Size          int64
    ContentType   string
    SourceVersion string
}

type Publication struct {
    RunID       string
    Result      UpsertResult
    PublishedAt time.Time
}

type PublishingStore interface {
    BeginRun(context.Context, Run) error
    StagePage(context.Context, string, uint64, Page) (UpsertResult, error)
    PublishRun(context.Context, string, uint64, time.Time) (Publication, error)
    FailRun(context.Context, string, uint64, string, time.Time) error
    LoadCursor(context.Context, string) (model.BillCursor, bool, error)
}

type QueryStore interface {
    Query(context.Context, Query) ([]model.CostLineItem, error)
    Summarize(context.Context, Query) ([]Summary, error)
}

type RunQuery struct {
    Provider     model.Provider
    AccountID    string
    Status       RunStatus
    BillingMonth string
    Offset       int
    Limit        int
}

type RunReader interface {
    ListRuns(context.Context, RunQuery) ([]Run, error)
}

type Store interface {
    PublishingStore
    QueryStore
    RunReader
}
```

Implement `Run.Validate`, `Page.Validate`, and legal transitions so only `pending -> running -> published|failed` is accepted. Add `ProviderAWS`, `ProviderAzure`, and `ProviderGCP` to `model.Provider.Validate` now so schema rows created in Phase 1 do not need an enum migration in Phase 3.

Add `RawArtifactKey string \`json:"rawArtifactKey,omitempty"\`` to `model.CostLineItem`. Publication copies the page artifact key onto every staged line item, which makes every returned fact traceable without exposing object-store credentials.

Define `RunQuery` with optional Provider, account, status, billing-period, offset, and limit filters. Extend `ledger.Query` with optional UTC `Start` and `End` fields while preserving zero values as the existing all-time behavior. Validation rejects one-sided or non-increasing windows. This gives MySQL summary and capacity tests a bounded time predicate without breaking compatibility callers.

`Run.ID` is the unique scheduler job ID, not a deterministic Provider/account/month key. A retry or expired-lease takeover reuses the same ID and cursor; a later correction or manual replay receives a new job/run ID so it can publish a new revision. Every successful job claim increments `LeaseEpoch`. `BeginRun` is idempotent only when every immutable field matches and adopts only a higher epoch; staging, failure, and publication reject stale epochs with `ledger.ErrStaleRun`. `StagePage` performs the `pending -> running` transition while holding the run lock.

- [ ] **Step 4: Run model and ledger tests**

Run: `go test ./pkg/cost/model ./pkg/cost/ledger -count=1`

Expected: PASS.

- [ ] **Step 5: Commit the contracts**

```bash
git add pkg/cost/model/types.go pkg/cost/model/types_test.go pkg/cost/ledger/store.go pkg/cost/ledger/run.go pkg/cost/ledger/run_test.go
git commit -m "refactor: define cost publication contracts"
```

---

### Task 2: Make the File Ledger a Publish-on-Complete Development Backend

**Files:**
- Modify: `pkg/cost/ledger/file.go`
- Modify: `pkg/cost/ledger/file_test.go`
- Modify: `pkg/cost/collector/collector.go`
- Modify: `pkg/cost/collector/collector_test.go`
- Create: `pkg/cost/ledger/contract/contract.go`

**Interfaces:**
- Consumes: `ledger.Store`, `ledger.Run`, and `ledger.Page` from Task 1.
- Produces: `contract.Run(t, factory)` and a file backend that never exposes staged items.

- [ ] **Step 1: Write a backend contract that proves publication isolation and correction history**

```go
type Factory func(t *testing.T) ledger.Store

func Run(t *testing.T, factory Factory) {
    t.Helper()
    t.Run("staged items are invisible", func(t *testing.T) {
        store := factory(t)
        run := fixtureRun("run-1")
        require.NoError(t, store.BeginRun(context.Background(), run))
        _, err := store.StagePage(context.Background(), run.ID, run.LeaseEpoch, ledger.Page{
            Number: 1, Items: []model.CostLineItem{fixtureItem("line-1", "10")}, Complete: true,
        })
        require.NoError(t, err)
        items, err := store.Query(context.Background(), ledger.Query{})
        require.NoError(t, err)
        require.Empty(t, items)
        _, err = store.PublishRun(context.Background(), run.ID, run.LeaseEpoch, time.Now().UTC())
        require.NoError(t, err)
        items, err = store.Query(context.Background(), ledger.Query{})
        require.NoError(t, err)
        require.Len(t, items, 1)
    })
}
```

The same contract must cover duplicate page submission, a new run replay with a changed amount, failed-run invisibility, cursor persistence, stale-epoch rejection, `ListRuns` status/timestamp filtering, and `Summarize` currency separation.

- [ ] **Step 2: Run the file contract and confirm it fails**

Run: `go test ./pkg/cost/ledger -run TestFileStoreContract -count=1`

Expected: FAIL because `FileStore` lacks the publication methods.

- [ ] **Step 3: Extend the snapshot and file implementation**

```go
type stagedRun struct {
    Run   Run          `json:"run"`
    Pages map[int]Page `json:"pages"`
}

type snapshot struct {
    Version int                           `json:"version"`
    Items   map[string]model.CostLineItem `json:"items"`
    Cursors map[string]model.BillCursor   `json:"cursors"`
    Runs    map[string]stagedRun           `json:"runs"`
}
```

Bump `snapshotVersion` to 2, migrate version 1 snapshots in memory, and write the migrated snapshot only on the next mutation. `PublishRun` must clone the snapshot, apply all staged pages in page-number order, update the cursor to complete, and perform one atomic rename.

- [ ] **Step 4: Change `Collector.PullMonth` to accept the scheduler run ID and stage pages**

```go
func (c *Collector) PullMonth(
    ctx context.Context,
    target Target,
    period model.BillingPeriod,
    runID string,
    leaseEpoch uint64,
) (Result, error) {
if err := c.store.BeginRun(ctx, ledger.Run{
    ID: runID, JobID: runID, LeaseEpoch: leaseEpoch,
    Provider: target.Provider, AccountID: target.AccountID,
    Period: period, Status: ledger.RunPending, CreatedAt: c.now().UTC(),
}); err != nil {
    return result, err
}
```

Load the cursor by `runID`, then stage each page after reconciliation. Publish only after the Provider returns `Complete=true`; call `FailRun` before returning terminal validation errors. The same run resumes from its cursor, while a new correction run starts at page one and updates facts by line-item idempotency key. Update collector tests to prove both behaviors.

- [ ] **Step 5: Run the complete cost core tests**

Run: `go test ./pkg/cost/ledger/... ./pkg/cost/collector/... -count=1`

Expected: PASS.

- [ ] **Step 6: Commit the development backend**

```bash
git add pkg/cost/ledger pkg/cost/collector
git commit -m "feat: publish cost runs atomically"
```

---

### Task 3: Add Embedded MySQL Migrations

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Create: `pkg/cost/ledger/mysql/migrate.go`
- Create: `pkg/cost/ledger/mysql/migrate_test.go`
- Create: `pkg/cost/ledger/mysql/migrations/0001_foundation.sql`
- Create: `pkg/cost/ledger/mysql/migrations/0002_partitions.sql`

**Interfaces:**
- Consumes: MySQL DSN and `database/sql`.
- Produces: `mysql.OpenDB(ctx, dsn) (*sql.DB, error)` and `mysql.Migrate(ctx, db) error`; Task 4 adds the ledger `Store` constructor.

- [ ] **Step 1: Add the pinned MySQL driver**

Run: `go get github.com/go-sql-driver/mysql@v1.9.3`

Expected: `go.mod` lists `github.com/go-sql-driver/mysql v1.9.3`.

- [ ] **Step 2: Write a migration idempotency test**

```go
func TestMigrateIsIdempotent(t *testing.T) {
    db := openTestDB(t)
    require.NoError(t, Migrate(context.Background(), db))
    require.NoError(t, Migrate(context.Background(), db))
    files, err := migrationFiles(migrationFS)
    require.NoError(t, err)
    var count int
    require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&count))
    require.Equal(t, len(files), count)
}
```

`openTestDB` must skip only when `CRANE_COST_MYSQL_DSN` is empty; CI sets it.

- [ ] **Step 3: Run the test against an empty MySQL database and confirm it fails**

Run: `CRANE_COST_MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/crane_cost?parseTime=true&multiStatements=true' go test ./pkg/cost/ledger/mysql -run TestMigrateIsIdempotent -count=1`

Expected: FAIL because `Migrate` and migrations do not exist.

- [ ] **Step 4: Implement the embedded, advisory-locked migration runner**

```go
//go:embed migrations/*.sql
var migrationFS embed.FS

func Migrate(ctx context.Context, db *sql.DB) error {
    conn, err := db.Conn(ctx)
    if err != nil {
        return fmt.Errorf("open migration connection: %w", err)
    }
    defer conn.Close()
    var acquired int
    if err := conn.QueryRowContext(ctx, `SELECT GET_LOCK('crane_cost_migrations', 60)`).Scan(&acquired); err != nil {
        return fmt.Errorf("acquire migration lock: %w", err)
    }
    if acquired != 1 {
        return errors.New("migration lock was not acquired")
    }
    defer conn.ExecContext(context.Background(), `SELECT RELEASE_LOCK('crane_cost_migrations')`)
    return applyEmbedded(ctx, conn, migrationFS)
}
```

Run the complete migration sequence on that dedicated `*sql.Conn`; MySQL DDL performs implicit commits, so do not claim file-level transactionality. Each statement must be restart-safe, and `schema_migrations` records the version, SHA-256 checksum, and completion time only after the whole file succeeds. Startup must fail if an already-applied filename's checksum changes.

The first migration must create `cloud_accounts`, `collection_runs`, `collection_cursors`, `billing_line_item_staging`, `raw_artifacts`, `billing_line_items`, `billing_line_item_revisions`, `estimated_cost_hourly`, `rate_cards`, `resource_mappings`, `cost_aggregates_daily`, `reconciliation_runs`, and `audit_events`. Store money as `DECIMAL(38,18)`, tags/raw metadata as JSON, all timestamps in UTC `DATETIME(6)`, and include `billing_month DATE` in every unique key required by partitioned tables. `estimated_cost_hourly` is created to enforce physical separation but is not written in Phase 1.

- [ ] **Step 5: Add current-month plus 24-month rolling partitions**

`0002_partitions.sql` must add monthly partitions to `billing_line_items` and `billing_line_item_revisions` and a `p_future` catch-all partition. Add `type ArtifactVerifier func(context.Context, string) error` and `MaintainPartitions(ctx, db, verify, now, 24)`; it reorganizes `p_future` before the next month begins and drops an expired partition only when every run represented in that partition has a `raw_artifacts` row whose object passes the verifier. Task 7 adapts `artifact.Store.Stat` to this function. Tests cover future-partition creation, the 24-month boundary, and refusal to drop when any object is absent.

- [ ] **Step 6: Run migration tests twice from a clean schema**

Run: `CRANE_COST_MYSQL_DSN='root:root@tcp(127.0.0.1:3306)/crane_cost?parseTime=true&multiStatements=true' go test ./pkg/cost/ledger/mysql -run TestMigrate -count=2`

Expected: PASS on both runs.

- [ ] **Step 7: Commit the schema**

```bash
git add go.mod go.sum pkg/cost/ledger/mysql
git commit -m "feat: add crane cost mysql schema"
```

---

### Task 4: Implement the MySQL Ledger and Backend Contract

**Files:**
- Create: `pkg/cost/ledger/mysql/store.go`
- Create: `pkg/cost/ledger/mysql/run.go`
- Create: `pkg/cost/ledger/mysql/query.go`
- Create: `pkg/cost/ledger/mysql/store_test.go`
- Modify: `pkg/cost/ledger/contract/contract.go`

**Interfaces:**
- Consumes: `ledger.Store` and migrations from Tasks 1 and 3.
- Produces: `mysql.New(db *sql.DB) (*Store, error)`, a concurrency-safe implementation of the same contract as `FileStore`.

- [ ] **Step 1: Run the shared contract against an empty MySQL implementation**

```go
func TestStoreContract(t *testing.T) {
    contract.Run(t, func(t *testing.T) ledger.Store {
        db := openTestDB(t)
        require.NoError(t, truncateAll(db))
        store, err := New(db)
        require.NoError(t, err)
        return store
    })
}
```

Run: `CRANE_COST_MYSQL_DSN="$CRANE_COST_MYSQL_DSN" go test ./pkg/cost/ledger/mysql -run TestStoreContract -count=1`

Expected: FAIL because MySQL Store methods are not implemented.

- [ ] **Step 2: Implement run creation and idempotent page staging**

Use one transaction per page. Lock the `collection_runs` row and matching epoch, reject published or failed runs, insert page artifact metadata into `raw_artifacts`, insert staged items keyed by `(billing_month, run_id, idempotency_key)` with their `raw_artifact_key`, and update the cursor in the same transaction. Replaying the same page with identical artifact checksums and items must return unchanged counts; a changed checksum under the same immutable key is a conflict.

```go
func (s *Store) StagePage(ctx context.Context, runID string, leaseEpoch uint64, page ledger.Page) (ledger.UpsertResult, error) {
    tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted})
    if err != nil { return ledger.UpsertResult{}, err }
    defer tx.Rollback()
    if err := lockWritableRun(ctx, tx, runID, leaseEpoch); err != nil { return ledger.UpsertResult{}, err }
    result, err := stageItems(ctx, tx, runID, page)
    if err != nil { return ledger.UpsertResult{}, err }
    if err := saveCursorTx(ctx, tx, runID, page.NextCursor, page.Complete); err != nil {
        return ledger.UpsertResult{}, err
    }
    return result, tx.Commit()
}
```

- [ ] **Step 3: Implement atomic publication and revision history**

`PublishRun` must lock and verify the current `lease_epoch`, upsert staged rows into `billing_line_items`, insert the old values into `billing_line_item_revisions` before each changed update, rebuild `cost_aggregates_daily` for every affected usage date/currency, mark the run published, and delete staging rows in one transaction. A second publication by the same epoch returns the existing publication without changing facts; an older epoch receives `ledger.ErrStaleRun`.

- [ ] **Step 4: Implement SQL-side filtering and aggregation**

Build queries only from a fixed field-to-SQL mapping. Never interpolate user values. `Summarize` must use the daily aggregate table for bounded compatible queries and SQL `SUM(list_cost)`, `SUM(net_cost)`, and `SUM(amortized_cost)` grouped by Provider, payer account, cluster, and currency for remaining compatibility queries; it must not load all rows into Go.

- [ ] **Step 5: Run race and conformance tests**

Run: `CRANE_COST_MYSQL_DSN="$CRANE_COST_MYSQL_DSN" go test -race ./pkg/cost/ledger/... -count=1`

Expected: PASS with both file and MySQL backends.

- [ ] **Step 6: Commit the MySQL ledger**

```bash
git add pkg/cost/ledger/mysql pkg/cost/ledger/contract
git commit -m "feat: persist published costs in mysql"
```

---

### Task 5: Add Raw Artifact Storage and S3-Compatible Archival

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`
- Modify: `pkg/cost/model/types.go`
- Modify: `pkg/cost/provider/aliyun/bills.go`
- Modify: `pkg/cost/provider/aliyun/aliyun_test.go`
- Modify: `pkg/cost/provider/tencent/bills.go`
- Modify: `pkg/cost/provider/tencent/tencent_test.go`
- Modify: `pkg/cost/provider/huawei/bills.go`
- Modify: `pkg/cost/provider/huawei/huawei_test.go`
- Modify: `pkg/cost/provider/volcengine/bills.go`
- Modify: `pkg/cost/provider/volcengine/volcengine_test.go`
- Modify: `pkg/cost/collector/collector.go`
- Modify: `pkg/cost/collector/collector_test.go`
- Create: `pkg/cost/artifact/store.go`
- Create: `pkg/cost/artifact/contract/contract.go`
- Create: `pkg/cost/artifact/file/store.go`
- Create: `pkg/cost/artifact/file/store_test.go`
- Create: `pkg/cost/artifact/s3/store.go`
- Create: `pkg/cost/artifact/s3/store_test.go`

**Interfaces:**
- Consumes: the exact raw Provider response bytes plus the staged-page artifact references from Tasks 1 and 4.
- Produces: `artifactfile.New(root string) (*Store, error)`, `artifacts3.New(config Config) (*Store, error)`, and immutable checksum-addressed artifacts linked to every published fact.

- [ ] **Step 1: Define and test the artifact contract**

```go
type Metadata struct {
    Key         string
    ETag        string
    SHA256      string
    Size        int64
    ContentType string
    CreatedAt   time.Time
}

type Store interface {
    Put(context.Context, string, io.Reader, int64, string, string) (Metadata, error)
    Open(context.Context, string) (io.ReadCloser, Metadata, error)
    Stat(context.Context, string) (Metadata, error)
}
```

The final `Put` argument is an optional expected SHA-256; an empty value means compute and return it. The contract must verify round-trip bytes, supplied-checksum rejection, SHA-256 validation on `Open`, immutable-key conflict behavior, missing keys, and context cancellation.

- [ ] **Step 2: Expose and archive exact Provider response bytes**

```go
type RawArtifact struct {
    Bytes         []byte `json:"-"`
    ContentType   string `json:"contentType"`
    SourceVersion string `json:"sourceVersion,omitempty"`
}

type BillPage struct {
    Items       []CostLineItem `json:"items"`
    RawArtifact RawArtifact    `json:"rawArtifact"`
    NextCursor  *BillCursor    `json:"nextCursor,omitempty"`
    RawCount    int            `json:"rawCount"`
    Complete    bool           `json:"complete"`
}
```

Change the four domestic `BillSource` implementations to decode from a retained copy of the exact HTTP response body and return it as `RawArtifact`; fixture tests assert byte equality. Before staging a page, `Collector` writes that payload under `<provider>/<account>/<billing-month>/<run-id>/<page>.raw`, converts returned metadata to `ledger.ArtifactRef`, and assigns its key to every item. In production mode, an empty payload or failed object write fails the run before `StagePage`; the compatibility file-development mode may explicitly set `artifacts.required=false`.

- [ ] **Step 3: Implement the file backend with atomic rename**

Write to a `0600` temporary file, stream-hash while copying, compare the expected checksum, fsync, rename, and fsync the parent directory. An existing key with a different checksum returns `artifact.ErrConflict`.

- [ ] **Step 4: Add and implement the pinned S3-compatible client**

Run: `go get github.com/minio/minio-go/v7@v7.0.95`

Configure endpoint, region, bucket, TLS, access-key file, secret-key file, and optional session-token file. `Put` must set `x-amz-meta-sha256`; `Open` and `Stat` must reject objects missing that metadata.

- [ ] **Step 5: Run Provider, collector, file, and MinIO contract tests**

Run: `go test ./pkg/cost/provider/... ./pkg/cost/collector ./pkg/cost/artifact/... -count=1`

Expected: file tests PASS; S3 tests PASS when `CRANE_COST_S3_ENDPOINT` is set and otherwise report SKIP.

- [ ] **Step 6: Commit artifact storage**

```bash
git add go.mod go.sum pkg/cost/model/types.go pkg/cost/provider pkg/cost/collector pkg/cost/artifact
git commit -m "feat: archive raw cost artifacts"
```

---

### Task 6: Implement MySQL Collection Leases and Worker Recovery

**Files:**
- Create: `pkg/cost/scheduler/types.go`
- Create: `pkg/cost/scheduler/repository.go`
- Create: `pkg/cost/scheduler/contract/contract.go`
- Create: `pkg/cost/scheduler/mysql.go`
- Create: `pkg/cost/scheduler/mysql_test.go`
- Create: `pkg/cost/scheduler/memory.go`
- Create: `pkg/cost/scheduler/memory_test.go`
- Create: `pkg/cost/scheduler/planner.go`
- Create: `pkg/cost/scheduler/planner_test.go`
- Create: `pkg/cost/scheduler/worker.go`
- Create: `pkg/cost/scheduler/worker_test.go`
- Create: `pkg/cost/provider/errors.go`
- Create: `pkg/cost/provider/errors_test.go`
- Create: `pkg/cost/ledger/mysql/migrations/0003_collection_jobs.sql`

**Interfaces:**
- Consumes: configured Provider/account targets and a `ledger.PublishingStore`.
- Produces: `scheduler.NewPlanner(repo Repository) *Planner`, `scheduler.NewMemoryRepository() Repository`, `scheduler.NewMySQLRepository(db *sql.DB) Repository`, and `scheduler.Worker` with daily job creation, lease recovery, fencing epochs, and typed retry decisions.

- [ ] **Step 1: Write concurrent claim and expired-lease tests**

```go
func TestClaimIsExclusiveAndExpiredLeaseCanBeRecovered(t *testing.T) {
    repo := newTestRepository(t)
    require.NoError(t, repo.Enqueue(ctx, fixtureJob("job-1")))
    now := time.Date(2026, 7, 20, 0, 0, 0, 0, time.UTC)
    first, err := repo.Claim(ctx, "worker-a", now, time.Minute)
    require.NoError(t, err)
    require.Equal(t, "job-1", first.ID)
    second, err := repo.Claim(ctx, "worker-b", now, time.Minute)
    require.ErrorIs(t, err, scheduler.ErrNoJob)
    recovered, err := repo.Claim(ctx, "worker-b", now.Add(61*time.Second), time.Minute)
    require.NoError(t, err)
    require.Equal(t, "job-1", recovered.ID)
    require.Equal(t, first.LeaseEpoch+1, recovered.LeaseEpoch)
}
```

- [ ] **Step 2: Add the append-only job migration and repository contract**

Create `0003_collection_jobs.sql`; never edit `0001_foundation.sql` or `0002_partitions.sql` after Task 3 has applied them. The new table has a unique job ID, Provider/account/month scope, job kind, request ID, creator identity, state, owner, `lease_epoch`, lease expiry, attempts, sanitized failure fields, `available_at`, created time, and completion time.

```go
type JobKind string
type JobState string

const (
    JobScheduled JobKind = "scheduled"
    JobReplay    JobKind = "replay"
    JobQueued    JobState = "queued"
    JobRunning   JobState = "running"
    JobCompleted JobState = "completed"
    JobFailed    JobState = "failed"
)

type Job struct {
    ID           string
    Kind         JobKind
    Provider     model.Provider
    AccountID    string
    Period       model.BillingPeriod
    RequestID    string
    CreatedBy    string
    State        JobState
    Attempt      int
    LeaseEpoch   uint64
    AvailableAt  time.Time
}

type StatusQuery struct {
    Provider     model.Provider
    AccountID    string
    BillingMonth string
    State        JobState
}

type JobStatus struct {
    ID               string
    State            JobState
    FailureClass     FailureClass
    Attempt          int
    ScheduledAt      time.Time
    LastTransitionAt time.Time
    AvailableAt      time.Time
}

type Repository interface {
    Enqueue(context.Context, Job) error
    Claim(context.Context, string, time.Time, time.Duration) (Job, error)
    Heartbeat(context.Context, string, string, uint64, time.Time, time.Duration) error
    Complete(context.Context, string, string, uint64, time.Time) error
    Fail(context.Context, string, string, uint64, Failure, time.Time) error
    ListStatuses(context.Context, StatusQuery) ([]JobStatus, error)
}
```

`StatusQuery` filters by Provider, account, billing month, and state without returning lease owners or error bodies; `JobStatus` contains state, failure class, attempt count, scheduled time, last transition time, and next availability. All time is passed explicitly and stored as UTC. Implement the same repository conformance suite against an in-memory repository for one-replica file development and MySQL for production. MySQL `Claim` uses a transaction and `SELECT ... FOR UPDATE SKIP LOCKED`, prefers the oldest `available_at`, and atomically sets owner, lease expiry, and `lease_epoch = lease_epoch + 1`. Heartbeat, completion, and failure match ID, owner, and epoch or return `scheduler.ErrLeaseLost`.

- [ ] **Step 3: Add the daily planner and idempotent job identity**

```go
type Target struct {
    Provider  model.Provider
    AccountID string
}

func (p *Planner) EnqueueDaily(ctx context.Context, now time.Time, targets []Target) error
func (p *Planner) EnqueueReplay(ctx context.Context, requestID, actor string, target Target, period model.BillingPeriod) (Job, error)
```

`EnqueueDaily` creates exactly the current UTC billing month and previous two billing months for every configured Provider/account. A scheduled job ID is the SHA-256 of Provider, account, billing month, and UTC schedule date, so both replicas can plan without duplicates. A manual replay requires a caller-generated request ID and actor, both persisted with the job, producing a distinct job/run ID and therefore a new correction revision. Tests cover month/year boundaries, two planners racing, 20 accounts, and repeated calls on the same day.

- [ ] **Step 4: Define error classes and retry policy**

```go
type FailureClass string
const (
    Authentication FailureClass = "authentication"
    RateLimited     FailureClass = "rate_limited"
    Transient       FailureClass = "transient"
    SchemaDrift     FailureClass = "schema_drift"
    InvalidData     FailureClass = "invalid_data"
    Permanent       FailureClass = "permanent"
)

type Failure struct {
    Class      FailureClass
    Message    string
    RetryAfter time.Duration
    Retry      bool
}
```

Define typed, sanitized `provider.AuthenticationError`, `provider.RateLimitError`, `provider.TransientError`, `provider.SchemaDriftError`, `provider.InvalidDataError`, and `provider.PermanentError` in `pkg/cost/provider/errors.go`; `scheduler.Classify(error) Failure` maps them without creating a package cycle. Authentication, SchemaDrift, InvalidData, and Permanent stop automatic retries. RateLimited honors `RetryAfter`; Transient uses capped exponential backoff with jitter. Persist class, sanitized message, next availability, and attempt count; never persist wrapped HTTP bodies or credentials.

- [ ] **Step 5: Implement worker heartbeat, cancellation, and publication fencing**

The worker claims one job, starts a heartbeat at one-third of the lease duration, and passes `Job.ID` plus `Job.LeaseEpoch` to `Collector.PullMonth`. It cancels processing if heartbeat reports lost ownership. It must never stage, publish, or mark a job complete after losing the lease; the ledger epoch check is the final protection if provider I/O ignores cancellation.

- [ ] **Step 6: Run migrations and scheduler tests with race detection**

Run: `CRANE_COST_MYSQL_DSN="$CRANE_COST_MYSQL_DSN" go test -race ./pkg/cost/ledger/mysql ./pkg/cost/scheduler/... -count=1`

Expected: PASS.

- [ ] **Step 7: Commit the scheduler**

```bash
git add pkg/cost/scheduler pkg/cost/provider/errors.go pkg/cost/provider/errors_test.go pkg/cost/ledger/mysql/migrations/0003_collection_jobs.sql
git commit -m "feat: lease cost collection jobs"
```

---

### Task 7: Wire Configurable Backends into the Service

**Files:**
- Modify: `pkg/cost/service/config.go`
- Create: `pkg/cost/service/config_test.go`
- Create: `pkg/cost/service/dependencies.go`
- Modify: `pkg/cost/service/service.go`
- Modify: `pkg/cost/service/service_test.go`
- Create: `pkg/cost/cmdutil/run.go`
- Create: `pkg/cost/cmdutil/run_test.go`
- Create: `cmd/crane-cost/main.go`
- Modify: `cmd/cost-collector/main.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: ledger, artifact, and scheduler implementations from Tasks 2-6.
- Produces: `service.OpenDependencies`, `service.NewWithDependencies`, and canonical `crane-cost` binary.

- [ ] **Step 1: Write configuration matrix tests**

```go
func TestConfigRequiresProductionBackendsForMultipleReplicas(t *testing.T) {
    cfg := validConfig()
    cfg.Storage.Backend = "file"
    cfg.Runtime.Replicas = 2
    require.ErrorContains(t, cfg.Validate(), "file backend requires one replica")
    cfg.Storage.Backend = "mysql"
    cfg.Storage.MySQL.DSNFile = "/secrets/mysql/dsn"
    cfg.Storage.MySQL.MigrationDSNFile = "/secrets/mysql/migration-dsn"
    cfg.Artifacts.Backend = "s3"
    cfg.Artifacts.Required = true
    require.NoError(t, cfg.Validate())
}
```

- [ ] **Step 2: Add explicit backend configuration**

```go
type StorageConfig struct {
    Backend string      `json:"backend"`
    File    FileConfig  `json:"file,omitempty"`
    MySQL   MySQLConfig `json:"mysql,omitempty"`
}

type FileConfig struct {
    Path string `json:"path"`
}

type MySQLConfig struct {
    DSNFile          string `json:"dsnFile"`
    MigrationDSNFile string `json:"migrationDsnFile"`
    MaxOpenConns     int    `json:"maxOpenConns"`
    MaxIdleConns     int    `json:"maxIdleConns"`
}

type ArtifactConfig struct {
    Backend string         `json:"backend"`
    Required bool          `json:"required"`
    File    ArtifactFile   `json:"file,omitempty"`
    S3      ArtifactS3     `json:"s3,omitempty"`
}

type ArtifactFile struct {
    Root string `json:"root"`
}

type ArtifactS3 struct {
    Endpoint         string `json:"endpoint"`
    Region           string `json:"region"`
    Bucket           string `json:"bucket"`
    UseTLS           bool   `json:"useTls"`
    AccessKeyFile    string `json:"accessKeyFile"`
    SecretKeyFile    string `json:"secretKeyFile"`
    SessionTokenFile string `json:"sessionTokenFile,omitempty"`
}

type SchedulerConfig struct {
    Workers       int    `json:"workers"`
    LeaseDuration string `json:"leaseDuration"`
    PollInterval  string `json:"pollInterval"`
    PlanInterval  string `json:"planInterval"`
}

type RuntimeConfig struct {
    Replicas int `json:"replicas"`
}
```

Keep `dataFile` as a deprecated compatibility input that maps to `storage.backend=file`; reject configurations that provide both forms. Production MySQL mode requires S3, `artifacts.required=true`, and `runtime.replicas >= 2`. File mode creates the in-memory job repository, forces one replica, and may explicitly disable required raw archives for compatibility tests. The planner always schedules the current month and previous two months daily; retain `lookbackDays` only for parsing old file-mode configurations and emit a deprecation log.

- [ ] **Step 3: Add dependency construction and injection**

```go
type Dependencies struct {
    Ledger    ledger.Store
    Artifacts artifact.Store
    Jobs      scheduler.Repository
    Ready     func(context.Context) error
    Maintain  func(context.Context, time.Time) error
    Now       func() time.Time
}

func NewWithDependencies(config Config, deps Dependencies, logger *log.Logger) (*Service, error)
func OpenDependencies(ctx context.Context, config Config) (Dependencies, func() error, error)
```

`MySQLConfig` has separate `migrationDsnFile` and `dsnFile` fields. `OpenDependencies` keeps a small migration/DDL pool only for startup migration and daily partition maintenance, opens a separate service read/write pool for normal work, verifies the artifact bucket, installs readiness and partition-maintenance functions, and returns a close function. Unit tests use `NewWithDependencies` and never need real external services.

For `storage.backend=file`, it opens the versioned file ledger, file artifact store, and `scheduler.MemoryRepository`. For `storage.backend=mysql`, it shares one `*sql.DB` between the ledger and scheduler repositories and verifies migrations before constructing the service. The close function closes every successfully opened dependency once, including partial-construction failures.

- [ ] **Step 4: Start API and workers under one cancellation tree**

Use `errgroup.WithContext`: one goroutine serves HTTP, one planner goroutine calls `EnqueueDaily` at startup and every `planInterval`, one maintenance goroutine calls `MaintainPartitions` at startup and every 24 hours, and configured worker goroutines run the scheduler. Any fatal infrastructure error cancels the group. Provider/account failures are job failures, not process-fatal errors. The handler resolves the configured Provider/account from the claimed job and calls `Collector.PullMonth` with its ID and epoch.

- [ ] **Step 5: Add the canonical binary without duplicating startup logic**

Move argument parsing and startup to an exported `cmdutil.Run(args, stdout, stderr) int` helper under `pkg/cost/cmdutil`. Both `cmd/crane-cost/main.go` and the compatibility `cmd/cost-collector/main.go` call it. Add `make crane-cost`; keep `make cost-collector` during the compatibility window.

The canonical binary also accepts `enqueue-replay --provider --account --month --request-id --actor`; it validates that the target exists, persists the replay job and actor through `Planner.EnqueueReplay`, then exits without starting HTTP workers. A repeated request ID is idempotent; a new request ID creates a new correction run. The compatibility binary rejects this subcommand and points operators to `crane-cost`.

- [ ] **Step 6: Run service tests and build both binaries**

Run: `go test ./pkg/cost/service ./pkg/cost/cmdutil -count=1`

Expected: PASS.

Run: `make crane-cost cost-collector`

Expected: `bin/crane-cost` and `bin/cost-collector` exist.

- [ ] **Step 7: Commit service wiring**

```bash
git add pkg/cost/service pkg/cost/cmdutil cmd/crane-cost cmd/cost-collector Makefile
git commit -m "feat: run crane cost with production backends"
```

---

### Task 8: Expose Run Freshness, Failure Class, and Readiness Metrics

**Files:**
- Modify: `pkg/cost/service/metrics.go`
- Modify: `pkg/cost/service/service.go`
- Modify: `pkg/cost/service/service_test.go`
- Create: `pkg/cost/service/metrics_test.go`

**Interfaces:**
- Consumes: published runs and scheduler job state.
- Produces: compatible Provider status responses plus account/month coverage and safe operational metrics.

- [ ] **Step 1: Write endpoint tests for partial provider failure**

```go
func TestReadyDoesNotRequireEveryAccountHealthy(t *testing.T) {
    service := newServiceWithInfrastructure(t, nil,
        providerStatus{Provider: model.ProviderAliyun, AccountID: "a", LastSuccess: fixedNow},
        providerStatus{Provider: model.ProviderTencent, AccountID: "b", LastErrorClass: scheduler.Authentication},
    )
    response := httptest.NewRecorder()
    service.handleReady(response, httptest.NewRequest(http.MethodGet, "/readyz", nil))
    require.Equal(t, http.StatusOK, response.Code)
}
```

Add companion cases where readiness is `200` before the first Provider success, `503` when the dependency readiness function fails, and `503` when the planner has not completed successfully within two plan intervals. Account authentication or schema-drift failures affect account status and metrics but never global readiness.

- [ ] **Step 2: Add bounded-cardinality metrics**

Add:

```text
crane_cost_collection_runs_total{provider,account_id,status,error_class}
crane_cost_collection_freshness_seconds{provider,account_id}
crane_cost_collection_lease_lost_total{provider}
crane_cost_artifact_write_errors_total{backend}
crane_cost_mysql_migration_version
```

Never label metrics with resource IDs, invoice IDs, run IDs, error messages, or credential paths.

- [ ] **Step 3: Return freshness and coverage from `/api/v1/providers`**

Build status from `ledger.ListRuns` plus `scheduler.Repository.ListStatuses`, so it survives process restarts. Extend each provider/account status with `coveredPeriods`, `freshnessSeconds`, `lastErrorClass`, and `lastRunStatus`. Preserve existing fields and JSON names; `lastError` contains only the classifier's sanitized public message, never a wrapped Provider response.

- [ ] **Step 4: Run service tests**

Run: `go test ./pkg/cost/service -count=1`

Expected: PASS.

- [ ] **Step 5: Commit observability**

```bash
git add pkg/cost/service
git commit -m "feat: report cost collection freshness"
```

---

### Task 9: Upgrade Helm and Manifests for Two-Replica MySQL Mode

**Files:**
- Modify: `charts/crane-cost-collector/values.yaml`
- Modify: `charts/crane-cost-collector/templates/configmap.yaml`
- Modify: `charts/crane-cost-collector/templates/deployment.yaml`
- Modify: `charts/crane-cost-collector/templates/networkpolicy.yaml`
- Modify: `charts/crane-cost-collector/templates/pvc.yaml`
- Create: `charts/crane-cost-collector/templates/poddisruptionbudget.yaml`
- Modify: `charts/crane-cost-collector/README.md`
- Modify: `deploy/cost-collector/configmap.yaml`
- Modify: `deploy/cost-collector/deployment.yaml`
- Modify: `deploy/cost-collector/networkpolicy.yaml`
- Modify: `deploy/cost-collector/secret.example.yaml`
- Modify: `deploy/cost-collector/README.zh.md`
- Create: `charts/crane-cost-collector/tests/mysql-values.yaml`
- Create: `charts/crane-cost-collector/tests/file-values.yaml`

**Interfaces:**
- Consumes: JSON configuration and secret-file paths from Task 7.
- Produces: two-replica production rendering and one-replica file development rendering.

- [ ] **Step 1: Add failing Helm render assertions**

Run:

```bash
helm template crane-cost charts/crane-cost-collector \
  --namespace crane-cost-system \
  -f charts/crane-cost-collector/tests/mysql-values.yaml | \
  rg 'replicas: 2|type: RollingUpdate|mysql/dsn|artifacts/s3'
```

Expected: FAIL because the chart still forces one replica and `Recreate`.

- [ ] **Step 2: Make MySQL/S3 mode the production default**

Set `replicaCount: 2`, `runtime.replicas: 2`, `storage.backend: mysql`, `artifacts.backend: s3`, and `artifacts.required: true`. Mount separate migration/service DSNs and S3 credentials from an existing Secret. Remove the ledger PVC in MySQL mode, use `RollingUpdate`, add readiness/liveness/startup probes, topology spread, anti-affinity, and a PDB with `minAvailable: 1`.

- [ ] **Step 3: Preserve explicit file development mode**

Render a PVC, enforce `replicaCount: 1`, and use `Recreate` only when `storage.backend=file`. Helm must fail when file mode requests multiple replicas or disables persistence.

- [ ] **Step 4: Restrict network access**

Keep ingress limited to the service port. Add explicit values for MySQL and S3 destination selectors or CIDRs/ports plus a cloud API HTTPS egress-proxy selector or CIDR. DNS remains allowed. Safe chart defaults render selectors for in-cluster `mysql`, `minio`, and `egress-proxy` Services but never `0.0.0.0/0`; operators must override them to match their environment. Document that direct access to changing cloud API addresses requires a CNI FQDN policy, otherwise configure `HTTPS_PROXY` to the selected egress proxy.

- [ ] **Step 5: Validate all supported Kubernetes versions**

Run: `helm lint charts/crane-cost-collector`

Expected: PASS.

Run: `for version in 1.34.8 1.35.5 1.36.1; do helm template crane-cost charts/crane-cost-collector --kube-version "$version" -f charts/crane-cost-collector/tests/mysql-values.yaml >/dev/null; done`

Expected: exit 0.

- [ ] **Step 6: Commit deployment changes**

```bash
git add charts/crane-cost-collector deploy/cost-collector
git commit -m "feat: deploy crane cost in ha mode"
```

---

### Task 10: Add Integration CI, Recovery Tests, and Operations Runbook

**Files:**
- Modify: `.github/workflows/go.yml`
- Modify: `.github/workflows/kubernetes-compatibility.yml`
- Create: `hack/cost/integration-test.sh`
- Create: `hack/cost/security-test.sh`
- Create: `hack/cost/load-test.go`
- Create: `docs/cost-platform-operations.zh.md`
- Modify: `charts/crane-cost-collector/README.md`

**Interfaces:**
- Consumes: production backends, service binary, metrics, and chart from Tasks 3-9.
- Produces: repeatable CI proof for migration, publication, lease recovery, HA, backup, and restore.

- [ ] **Step 1: Add MySQL and MinIO services to CI**

Configure `mysql:8.0.43` with a health check and start `minio/minio:RELEASE.2025-09-07T16-13-09Z` in a workflow step. Export `CRANE_COST_MYSQL_DSN`, `CRANE_COST_S3_ENDPOINT`, bucket, and test credentials only to cost package tests. CI first migrates an empty schema twice, then verifies an applied migration checksum change is rejected and the append-only `0003` migration upgrades a schema that already has `0001` and `0002`.

- [ ] **Step 2: Write the end-to-end recovery script**

The script must:

1. Start two `crane-cost` processes against one MySQL database.
2. Serve a two-page fake Provider response.
3. Suspend the lease owner after page one so its heartbeat expires.
4. Verify the second process takes over within 60 seconds.
5. Verify staged rows remain invisible before publication.
6. Resume the stale first process and verify epoch fencing rejects its publication.
7. Verify the archived bytes equal the fake Provider response and every fact references that artifact.
8. Verify exactly two published rows after completion.
9. Enqueue a replay with a corrected amount and verify one revision per changed row.
10. Restart both processes and verify facts, replay actor/request ID, and provider freshness persist.
11. Verify the planner creates only current-month and prior-two-month jobs on both replicas.

Run: `bash hack/cost/integration-test.sh`

Expected: exit 0 and a final `PASS: crane-cost recovery and publication` line.

- [ ] **Step 3: Add the capacity harness**

`hack/cost/load-test.go` must generate deterministic synthetic rows across 20 accounts and 50 clusters, batch insert them, query daily summaries and 200-row detail pages, and print machine-readable p50/p95 values for both query classes. CI runs a reduced 100,000-row smoke profile; release qualification runs the 10,000,000-row profile.

Run: `go run ./hack/cost/load-test.go --accounts=20 --clusters=50 --rows=100000 --dsn="$CRANE_COST_MYSQL_DSN"`

Expected: exit 0, no duplicate keys, summary p95 below 1 second, and 200-row detail p95 below 2 seconds.

- [ ] **Step 4: Document production operations**

The runbook must include credential rotation, failed-account diagnosis, schema-drift quarantine, the exact `crane-cost enqueue-replay` command, monthly partition maintenance, MySQL backup/restore, raw artifact verification, disaster recovery, and use of the compatibility file backend for development only. Include separate migration and service read/write MySQL grants and a report-read-only grant template.

- [ ] **Step 5: Add the credential-leak security regression**

`hack/cost/security-test.sh` configures sentinel credential values, runs validation, collection failure, metrics, and provider-status requests, then asserts the sentinels are absent from captured logs, MySQL text/JSON columns, metrics, and HTTP bodies.

- [ ] **Step 6: Run the complete Phase 1 verification**

Run:

```bash
find pkg/cost cmd/crane-cost cmd/cost-collector hack/cost -name '*.go' -print0 | xargs -0 gofmt -w
go vet ./pkg/cost/... ./cmd/crane-cost ./cmd/cost-collector
go test -race ./pkg/cost/...
make crane-cost cost-collector
helm lint charts/crane-cost-collector
bash hack/cost/integration-test.sh
bash hack/cost/security-test.sh
git diff --check
```

Expected: every command exits 0; all tests report PASS; Helm reports zero failures; the integration script reports its final PASS line.

- [ ] **Step 7: Commit CI and operations documentation**

```bash
git add .github/workflows hack/cost docs/cost-platform-operations.zh.md charts/crane-cost-collector/README.md
git commit -m "test: verify crane cost platform foundation"
```

---

## Phase 1 Completion Gate

Do not begin the Phase 2 domestic Provider migration plan until all of the following are evidenced:

- File and MySQL backends pass the same publication contract.
- Staged runs remain invisible and publish atomically.
- Corrections create revision history without duplicate current facts.
- Two workers cannot own one live lease, and expired leases recover within 60 seconds.
- A stale lease epoch cannot stage or publish after another worker takes ownership.
- Both replicas plan one daily job per Provider/account for exactly the current and previous two billing months.
- Exact raw Provider responses round-trip through file and S3-compatible backends with verified SHA-256, and every published fact references an archived object.
- Two service replicas pass readiness and continue serving queries during one-replica termination.
- The compatibility APIs and `cost-collector` binary remain available.
- Applied migrations are checksum-protected and all later schema changes are append-only.
- The 100,000-row CI profile and 10,000,000-row release profile meet both approved summary and detail query targets across 20 accounts and 50 clusters.
- Security scans find no credential values in logs, metrics, API responses, or MySQL rows.
