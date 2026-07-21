# Crane 多云成本平台设计

> **已部分取代（2026-07-21）：** 本文关于独立 `crane-cost` 服务、运行拓扑和
> Phase 1 至 Phase 4 交付顺序的决策，已由
> [Crane OpenCost C/S FinOps 平台设计](./2026-07-21-crane-opencost-cs-finops-platform-design.md)
> 取代。精确金额、多云 Provider、账单修订、对象归档和对账要求继续有效。

## 1. 背景与结论

本文设计一套以 Crane 为统一产品入口和优化控制面的多云成本系统。系统面向企业内部最多 50 个 Kubernetes 集群、20 个云账号，覆盖 AWS、Azure、GCP、阿里云、腾讯云、华为云和火山引擎。

最终决策是以 Crane 为主进行扩展，不把 OpenCost 作为运行主干，也不把 OpenCost 源码合并进 Crane。OpenCost 仅用于参考成本领域划分、FOCUS 对齐方式、Kubernetes Allocation、Asset 和 idle/share 等模型。

Crane 的目标闭环是：

1. 采集并标准化七家云的实付账单和估算价格。
2. 保留小时级估算和 T+1/T+2 实付账单两套独立成本口径。
3. 在后续子项目中完成 Kubernetes 成本分摊。
4. 使用成本对 Crane Recommendation 进行节省评估和优先级排序。
5. 由用户审批或 Preview 后执行优化，不让账单金额直接驱动无人值守扩缩容。

## 2. 现状与选型依据

### 2.1 本地 Crane 现状

当前分支已经包含独立 `cost-collector` 和以下成本领域代码：

- [成本模型](../../../pkg/cost/model/types.go)
- [Provider 接口](../../../pkg/cost/provider/provider.go)
- [Provider 注册](../../../pkg/cost/providers/register.go)
- [采集器](../../../pkg/cost/collector/collector.go)
- [文件账本](../../../pkg/cost/ledger/file.go)
- [成本分配函数](../../../pkg/cost/allocation/allocation.go)
- [资源映射](../../../pkg/cost/reconcile/reconcile.go)
- [HTTP 服务](../../../pkg/cost/service/service.go)

阿里云、腾讯云、华为云和火山引擎 Provider 已实现账单 API、分页、签名、标准化和契约测试。现有 `pkg/cost/...` 测试在允许本机回环监听后全部通过。

现状仍有以下限制：

- 生产账本是单副本文件存储，Deployment 使用 `Recreate`。
- 费率主要依赖配置的合同价或自定义价目表。
- `/api/v1/allocate` 需要调用方提供权重，不会自动完成 Kubernetes 使用量分摊。
- 账单服务和 Recommendation、EHPA、Dashboard 之间没有稳定的领域接口。
- 账单汇总指标与现有 Dashboard 的节点小时价格指标并不是同一种成本口径。

### 2.2 OpenCost 对比结论

核对时间为 2026-07-20。OpenCost 最新官方发布为 `v1.120.4`，官方 README 和文档显示其优势包括：

- Kubernetes cluster、node、namespace、controller、service、pod 和 container 成本分摊。
- CPU、GPU、内存、PV、网络、闲置资源和 Asset 成本模型。
- AWS、Azure、GCP 的正式 Cloud Cost 配置和账单查询。
- `/allocation`、`/assets`、`/cloudCost` 等查询 API。
- FOCUS 对齐的 Custom Cost 插件机制。

不选择 OpenCost 作为主干的原因是：

- 官方完整云账单配置主要覆盖 AWS、Azure 和 GCP。
- OpenCost 虽有阿里云节点/PV 定价和 BOA 代码，但当前 `CloudCostIntegration` 工厂对 Alibaba BOA 返回 `nil`。
- 腾讯云、华为云和火山引擎没有对应的完整官方账单集成。
- OpenCost Custom Cost 插件结果默认存放在内存仓库，Pod 重启后需要重新拉取，不适合作为财务账本。
- OpenCost 不承担 Crane Recommendation、EHPA、EVPA、预测弹性及优化执行职责。

参考资料：

- [OpenCost README](https://github.com/opencost/opencost/blob/develop/README.md)
- [OpenCost API](https://www.opencost.io/docs/integrations/api)
- [OpenCost AWS 配置](https://www.opencost.io/docs/configuration/aws)
- [OpenCost Azure 配置](https://www.opencost.io/docs/configuration/azure)
- [OpenCost GCP 配置](https://www.opencost.io/docs/configuration/gcp)
- [OpenCost Plugins](https://github.com/opencost/opencost-plugins)

### 2.3 方案对比

| 方案 | 做法 | 优点 | 主要问题 |
|---|---|---|---|
| OpenCost 为主 | 国内四家开发成 OpenCost Provider 或 Plugin | 成本模型、Kubernetes 分摊和 API 统一 | 国内 Provider 缺口、插件内存存储、需要维护 OpenCost 分支 |
| Crane 为主，全部内置 | 把采集、账本、分摊、推荐和 API 全部放进 `craned` | 单进程、表面上部署简单 | 职责耦合、故障域扩大、凭证进入优化控制面、无法独立扩缩容 |
| Crane 产品主导，独立成本域 | Crane 提供统一产品和 API，独立 `crane-cost` 负责成本 | 保持 Crane 主导，成本域可独立演进和扩缩容 | 新增一个内部服务和稳定接口 |

采用第三种方案。

## 3. 范围与拆分

完整系统拆成五个独立子项目：

1. 成本平台底座：MySQL、对象存储、任务调度、统一成本模型和 API。
2. 七家云 Provider：账单、费率、身份映射、限流、重试和回溯。
3. Kubernetes 成本分摊：Node、PV、网络、控制面、闲置资源和工作负载归因。
4. 成本优化闭环：预计节省、风险、置信度、优先级、审批和节省验收。
5. 统一产品与治理：Dashboard、预算、异常检测、告警、RBAC 和审计。

本文只定义前两个子项目及其 Crane API 接入，形成 Phase 1 至 Phase 4 的单一实施范围。Kubernetes 分摊和优化机会评分另立设计文档，本文仅预留稳定接口。

### 3.1 本期包含

- 现有 `cost-collector` 演进为中心化 `crane-cost`。
- MySQL 8 生产存储和现有文件存储开发模式。
- 对象存储原始账单归档。
- 七家云 Provider。
- 多币种、汇率、补账、退款和摊销成本。
- `craned` 到 `crane-cost` 的稳定内部 API。
- 云账号管理、采集状态、成本查询、聚合和基础对账 API。
- 基础成本管理页面所需的后端能力。

### 3.2 本期不包含

- Pod、Namespace、Workload 的自动成本分摊。
- `OptimizationOpportunity` CRD 和收益评分引擎。
- 成本直接参与 EHPA 实时控制。
- 预算和异常检测。
- 面向外部客户的多租户 SaaS 隔离。
- ClickHouse、Kafka 或其他新增数据基础设施。

## 4. 总体架构

```text
Crane Dashboard
      |
    craned API
      |
      +---- Recommendation / EHPA / EVPA
      |
      +---- crane-cost API
                 |
          +------+-------+
          |              |
    Provider Workers   Query/Aggregation
          |              |
 七家云账单/价目表   MySQL + Object Storage
```

### 4.1 `crane-cost`

`crane-cost` 由现有 `cmd/cost-collector` 演进而来，是独立进程：

- 负责云账号管理、采集调度、Provider 执行、标准化、持久化、聚合和对账。
- 生产部署两个副本；HTTP API 可同时提供服务，采集任务通过 MySQL 租约保证单任务单 Worker。
- 文件账本保留为显式开发模式，生产默认使用 MySQL。
- 原始 CUR、CSV 或 API 响应归档到对象存储。
- 只通过 Secret 或 Vault 引用获取云凭证，MySQL 不保存 AccessKey 明文。

### 4.2 `craned`

`craned` 保持优化控制面职责：

- 不直接连接成本数据库，不持有云凭证。
- 使用版本化内部 Cost API 获取成本、费率、覆盖率和对账结果。
- 对 Dashboard 暴露统一 API 并执行用户权限过滤。
- `crane-cost` 故障时，Recommendation 和 EHPA 继续运行，成本排序和收益评估显示为不可用。

### 4.3 Dashboard

Dashboard 只访问 `craned`，不感知 MySQL 或 `crane-cost` 的部署拓扑。第一阶段提供云账号、采集健康、账单汇总、明细查询和基础对账入口。后续成本分摊与优化机会继续复用同一 API 命名空间。

### 4.4 代码边界

目标代码结构如下：

```text
cmd/crane-cost/                  中心成本服务入口
pkg/cost/model/                  统一领域模型和 DTO
pkg/cost/provider/               Provider SPI 和七家云适配器
pkg/cost/ledger/mysql/           MySQL 账本
pkg/cost/ledger/file/            显式开发模式文件账本
pkg/cost/scheduler/              任务创建、租约、重试和回溯
pkg/cost/artifact/               对象存储归档接口
pkg/cost/reconciliation/         批次和账期对账
pkg/cost/service/                内部 Cost API
pkg/server/handler/cost/         craned 统一 API 和权限过滤
pkg/web/                         成本页面
```

实施时不同时保留两个功能重复的生产二进制。`cmd/cost-collector` 在兼容期作为 `crane-cost` 的别名或迁移入口，最终由发布说明明确废弃周期。

## 5. 统一成本模型

### 5.1 事实口径

两类事实必须物理分表：

- `estimated_cost_hourly`：小时级资源价格和使用量估算，后续用于优化排序。
- `billing_line_items`：T+1/T+2 实付账单，保存 list、net 和 amortized 成本，用于财务对账和节省验收。

查询层允许对比但禁止直接相加。所有 API 必须显式返回 `costKind=estimated|billed`。

### 5.2 MySQL 表

| 表 | 用途 |
|---|---|
| `cloud_accounts` | Provider、付款账号、凭证引用和采集配置 |
| `collection_runs` | 采集窗口、游标、租约、状态、错误和重试次数 |
| `raw_artifacts` | 对象地址、ETag、校验和和源版本 |
| `billing_line_items` | 实付账单事实 |
| `billing_line_item_revisions` | 补账、退款和修订审计 |
| `estimated_cost_hourly` | 后续 Kubernetes 分摊的小时级估算事实 |
| `rate_cards` | 合同价、目录价、有效期和来源 |
| `resource_mappings` | 云资源、账号、集群和业务标签映射 |
| `cost_aggregates_daily` | 日级常用维度聚合 |
| `reconciliation_runs` | 估算与实付差异、覆盖率和未分摊金额 |
| `audit_events` | 账号、汇率、映射和重放操作审计 |

### 5.3 账单行项目

`billing_line_items` 采用 FOCUS 对齐的公共字段：

- Provider、付款账号、使用账号、服务、产品、SKU 和资源 ID。
- Region、Zone、计费模式、费用类别、使用量和单位。
- `list_cost`、`net_cost` 和 `amortized_cost`。
- 原币种、报表币种、汇率、汇率日期和汇率来源。
- 使用开始/结束时间、账期、发票号、标签和采集时间。
- 采集批次、原始对象引用、源版本和标准化器版本。

金额使用定点 `DECIMAL`，Go 领域模型继续使用精确 Decimal，不允许用 `float64` 保存或计算财务金额。

### 5.4 幂等和修订

幂等键优先使用云厂商稳定行项目 ID。没有稳定 ID 时，由 Provider、账号、账期、资源、SKU、使用窗口和稳定标签计算哈希。金额不进入幂等键，因此补账、退款或折扣修订会更新当前事实，并在修订表保留旧版本。

事实表按账期月份分区。默认在线保留 24 个月明细，原始账单在对象存储长期保留，并可按采集批次重放。

### 5.5 多币种

- 原始金额和原币种永不覆盖。
- 统一报表金额保存汇率、汇率日期和来源。
- 缺少有效汇率时不允许跨币种聚合。
- 汇率修订产生新的聚合版本并记录审计事件，不修改原始账单金额。

## 6. 采集数据流

```text
获取账单/API
  -> 原始响应归档
  -> Provider 标准化
  -> staging 批次校验
  -> 幂等写入事实表
  -> 更新日级聚合
  -> 生成采集覆盖率和对账状态
```

每个账号每天采集当前账期和前两个账期，以处理延迟账单、退款和折扣修订。每页完成后保存游标，Worker 重启后从最后完成页继续。

标准化结果先进入逻辑 staging 批次。只有整批完成金额、币种、时间窗口和必填身份字段校验后，才切换为可查询版本，避免用户看到半批数据。

一个账号失败不阻塞其他账号，一个账期失败不回滚其他账期。日级聚合从已发布事实构建，聚合版本记录来源批次，便于回溯。

## 7. 七家云 Provider

### 7.1 Provider SPI

```go
type Provider interface {
    ValidateCredential(ctx context.Context) error
    Capabilities() Capabilities
    PullBills(ctx context.Context, cursor BillCursor) (BillPage, error)
    GetRates(ctx context.Context, query RateQuery) ([]Rate, error)
    ResolveIdentity(ctx context.Context, resource ResourceRef) (CloudIdentity, error)
}
```

Provider 只负责云端协议、鉴权、分页和标准化，不负责数据库事务、任务租约、通用重试、汇率转换或业务分摊。

`Capabilities` 必须明确账单 API、账单文件、费率和身份解析是否可用。缺少真实价格时返回不可用，不得使用虚构默认值。

### 7.2 数据源

| 云厂商 | 实付账单主来源 | 估算价格来源 |
|---|---|---|
| AWS | CUR，经 S3/Athena | Price List、合同价覆盖 |
| Azure | Cost Management Export/Blob | Retail Price、Price Sheet |
| GCP | Detailed Billing Export/BigQuery | Cloud Billing Catalog、合同价 |
| 阿里云 | BSS OpenAPI 账单明细 | DescribePrice、合同价 |
| 腾讯云 | Billing 账单明细 API | 询价 API、合同价 |
| 华为云 | BSS 账单明细 API | 价格目录、合同价 |
| 火山引擎 | Billing 账单明细 API | 询价 API、合同价 |

Phase 2 迁移并加固现有国内四家 Provider；Phase 3 新增 AWS、Azure 和 GCP。国际云实现可参考 OpenCost 的字段映射和测试用例，但不得把 OpenCost 内部 Go 类型作为 Crane 公共接口。

## 8. 调度与容错

### 8.1 任务租约

MySQL 8 任务表承担调度，不引入 Kafka。Worker 使用 `FOR UPDATE SKIP LOCKED` 领取任务，租约包含 owner、过期时间和心跳。Worker 异常退出后，其他副本可以领取过期任务。

任务粒度为 Provider、账号和账期。相同任务同一时间只允许一个 Worker 执行，发布事实时再次校验批次版本以防止过期 Worker 覆盖新结果。

### 8.2 错误分类

| 类型 | 行为 |
|---|---|
| `Authentication` | 停止自动重试，标记账号不可用并告警 |
| `RateLimited` | 尊重云端退避时间，指数退避并增加随机抖动 |
| `Transient` | 网络或 5xx，有限次数重试 |
| `SchemaDrift` | 归档原始响应，隔离批次并高优先级告警 |
| `InvalidData` | 记录脱敏样本，不写入正式事实表 |
| `Permanent` | 等待人工修复配置、权限或不支持的能力 |

### 8.3 健康语义

服务就绪条件是 MySQL 可用、迁移版本兼容且调度器工作，不要求所有账号健康。账号级状态单独暴露 freshness、最后成功时间、覆盖账期、采集延迟、错误分类和行数。

日志、指标和错误信息禁止出现凭证、完整账单响应或个人标识。Provider 在每次任务开始时重新读取凭证，支持无重启轮换。

## 9. API 与 Crane 集成

### 9.1 API 分组

| API | 用途 |
|---|---|
| `/api/v1/cost/accounts` | 云账号配置、凭证引用和连接验证 |
| `/api/v1/cost/collections` | 采集状态、覆盖范围、手动回溯和重试 |
| `/api/v1/costs` | 明细查询和游标分页 |
| `/api/v1/costs/summary` | 按云、账号、服务、区域、资源和标签聚合 |
| `/api/v1/cost/rates` | 指定时间点的资源价格 |
| `/api/v1/cost/reconciliation` | 差异、未映射金额和覆盖率 |
| `/api/v1/cost/providers` | Provider 能力、健康状态和 freshness |

查询必须显式指定：

- 时间窗口。
- `costKind=estimated|billed`。
- `costMetric=list|net|amortized`。
- 原币或统一报表币种。
- 聚合维度和过滤条件。

响应返回数据 freshness、覆盖率和来源。大范围明细查询使用异步导出，不允许同步扫描全部事实表。内部 API 使用版本化 DTO，不暴露数据库表结构。

### 9.2 安全和权限

- Dashboard 用户通过 Crane 现有认证进入。
- `craned` 到 `crane-cost` 使用 ServiceAccount Token 或 mTLS。
- 云账号操作需要独立 `cost-admin` 权限。
- 普通用户只能读取授权集群和成本中心。
- 凭证 API 只接受 Secret 或 Vault 引用，永不返回凭证内容。
- 删除云账号采用软删除，停止采集但保留历史成本和审计记录。
- 手动重放、汇率和资源映射变更记录审计事件。
- MySQL 使用迁移、服务读写和报表只读三个独立账号。

## 10. 测试设计

### 10.1 测试层次

1. Provider 契约测试：使用脱敏固定响应验证签名、分页、金额、退款、摊销、币种和错误分类。
2. 重放测试：重复采集结果不变，补账只产生预期修订，中途失败可从游标恢复。
3. MySQL 集成测试：真实 MySQL 8 验证迁移、租约竞争、分区、事务、并发写入和故障恢复。
4. API 测试：鉴权、RBAC、分页、聚合、币种转换、覆盖率和 `craned` 代理兼容性。
5. 故障注入：限流、超时、凭证过期、Schema Drift、对象存储失败、Worker 被杀和数据库短暂不可用。

PR CI 不调用真实云账号。Provider 使用脱敏 fixture 和本地协议模拟器；独立受控环境执行周期性真实账号冒烟测试。

### 10.2 数据不变量

- 同一采集批次重放不得产生重复成本。
- 退款和负金额保持符号。
- 原始行项目汇总等于发布批次汇总，误差不超过币种最小单位。
- 缺少汇率时拒绝跨币种聚合。
- 每个报表结果可追溯到采集批次和原始对象。
- 未映射成本必须显示为 `unallocated`，不能静默丢弃。

## 11. 验收标准

### 11.1 容量和性能

- 支持 20 个云账号。
- 支持每月 1,000 万条账单明细。
- 在线保留 24 个月，最近三个月为高频工作集。
- 日级汇总查询 p95 小于 1 秒。
- 200 条明细分页查询 p95 小于 2 秒。
- Worker 异常退出后 60 秒内由其他副本接管。
- 两副本并发运行不产生重复批次或重复成本。

### 11.2 正确性

- 各账号月度 `net_cost` 和 `amortized_cost` 与云厂商汇总差异不超过最小货币单位。
- 退款、折扣、补账和跨月修订可重放并留存审计记录。
- Provider 能力缺失时明确显示不可用，不伪造价格。
- 所有查询明确区分 estimated 和 billed。
- `crane-cost` 不可用不影响 Recommendation 和 EHPA 的基本运行。

### 11.3 安全

- 数据库、日志、指标、API 和错误响应中不存在云凭证明文。
- 普通用户无法查看未授权账号、集群或成本中心。
- 所有管理和重放操作有操作者、时间、对象和结果审计。
- 容器继续满足 Restricted Pod Security，默认只读根文件系统和非 root 用户。

## 12. 分阶段交付

### Phase 1：成本平台底座

- 抽象 Store 和 ArtifactStore 接口。
- 建立 MySQL Schema、迁移工具、月份分区和数据保留任务。
- 建立任务租约、批次发布、游标和错误分类。
- 建立对象存储归档和重放能力。
- 保留文件账本开发模式。

### Phase 2：国内四家 Provider

- 迁移阿里云、腾讯云、华为云和火山引擎 Provider。
- 补全账号连接验证、Capabilities 和错误分类。
- 保持现有只读 API 的兼容窗口。
- 完成真实脱敏账单的月度金额验收。

### Phase 3：国际三家 Provider

- AWS CUR、Azure Cost Management Export 和 GCP Detailed Billing Export。
- 多币种和汇率版本。
- 统一 Provider 契约和七家云覆盖率页面。

### Phase 4：Crane 产品接入

- `craned` Cost Client、统一 API、RBAC 和审计。
- 云账号、采集状态、账单汇总、明细和基础对账页面。
- 两副本生产部署、监控、告警、备份和恢复演练。

### 后续独立设计

1. Kubernetes 成本分摊：采集使用量，计算 Node、PV、网络、控制面和闲置成本，映射到 cluster、namespace、workload、pod、label 和成本中心。
2. 成本优化闭环：定义 `OptimizationOpportunity`，将 Crane Recommendation 与价格和分摊结果结合，计算预计月节省、风险、置信度和优先级。
3. 治理：预算、异常检测、告警和节省验收。

## 13. 实施约束

- 不把账单明细写入 Kubernetes CRD 或 Prometheus。
- 不让 Dashboard 直接访问 MySQL。
- 不让 `craned` 持有云凭证。
- 不在 Provider 内实现通用调度、持久化和业务分摊。
- 不让成本金额直接控制 EHPA。
- 不在 Phase 1 至 Phase 4 引入 ClickHouse、Kafka 或 OpenCost 运行依赖。
- 不复制 OpenCost 内部类型形成不可升级的源码耦合。

这些约束保证 Crane 是完整产品和控制面，同时让成本域保持独立故障边界、稳定接口和后续演进空间。
