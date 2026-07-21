# Crane OpenCost C/S FinOps 平台设计

## 1. 文档状态

本文是 Crane 企业级多集群 FinOps 平台的总体架构设计，已于 2026-07-21
完成用户评审。

本文取代以下文档中关于运行拓扑、服务边界和交付顺序的决策：

- `2026-07-20-crane-multicloud-cost-platform-design.md` 中独立部署
  `crane-cost` 的设计。
- `2026-07-20-crane-cost-platform-foundation.md` 中以独立成本服务为目标的
  实施计划。

旧文档中的精确金额、多云 Provider、账单修订、对象归档和对账要求继续有效，
但其实现必须落入本文定义的中心 `crane-server`，不能新增集群侧 OpenCost 服务。

本文是跨阶段总体设计，不作为一次性大版本的实施清单。每个交付阶段必须单独
形成可执行计划并通过相应验收门槛。第一个实施计划只覆盖 C/S 基础。

## 2. 背景与目标

Crane 已具备节点资源采集、Prometheus 数据源、Recommendation、EHPA、EVPA、
预测扩缩容和基础成本模块，但当前能力仍以单集群为中心：

- `crane-agent` 是逐节点 DaemonSet，主要负责 cAdvisor/CRI 数据采集、QoS
  分析和本地执行。
- `craned` 同时运行 Kubernetes 控制器、预测器、推荐器和单集群 API。
- 当前 Cluster Store 只保存集群 URL，Namespace 和 Recommendation API 仍使用
  `craned` 所在集群的 Kubernetes Client，不是真正的多集群控制面。
- 成本 Dashboard 主要依赖 PromQL 和配置价格，无法稳定处理账单修订、Idle、
  Shared Cost、PV、网络以及企业成本中心。
- 已有 `cost-collector` 使用文件账本，无法承担多副本中心服务和企业财务对账。
- 当前 Recommendation 采纳接口可直接 Patch 工作负载，缺少中心计划版本、审批、
  过期、前置条件、幂等和回滚协议。

目标是形成一套中心 Server 与集群 Crane 组件协作的 C/S 平台：

1. 最多 50 个 Kubernetes 集群通过出站连接接入中心。
2. 集群内不部署 OpenCost，不新增消息队列、数据库或成本计算服务。
3. Crane 内化 OpenCost 的 Allocation、Asset、Idle 和 Shared Cost 语义。
4. 中心统一完成多集群成本分析、云账单对账、推荐、预算和治理。
5. 集群内 Crane 继续负责 Recommendation、EHPA、EVPA、QoS 和最终执行。
6. 成本优化遵循 Observe、Preview、Approval、Auto Allowlist 的安全演进路径。
7. 中心不可用时，集群现有扩缩容和安全控制不受影响。

## 3. 非目标

- 不把 OpenCost 作为独立进程、Sidecar 或集群服务部署。
- 不让中心保存业务集群 kubeconfig 或直接调用业务 Kubernetes API。
- 不上传完整原始 Prometheus 时序到中心。
- 不把账单明细或小时分摊事实存入 Prometheus、ConfigMap 或 CRD。
- 第一阶段不引入 Kafka、ClickHouse、Thanos 或新的集群侧数据库。
- 不让成本金额绕过 SLO、PDB 和资源安全下限直接控制 EHPA。
- 不在一个实施计划中同时完成成本、优化闭环、预算和全部 Dashboard。

## 4. 方案选择

### 4.1 候选方案

| 方案 | 数据处理位置 | 优点 | 问题 |
|---|---|---|---|
| 全量原始数据上传 | Agent 上传原始指标，中心完成全部处理 | 算法版本完全统一 | 高基数数据量大，跨集群网络和中心存储压力不可控 |
| 每集群完整成本引擎 | 每个集群运行 OpenCost 风格引擎 | 数据本地化，中心负担较小 | 增加服务，算法升级漂移，国内云账单仍需中心处理 |
| 边缘汇聚、中心计算 | 节点 Agent 采集，`craned` 汇总用量，中心分摊 | 集群组件最少、中心算法统一、可离线回补 | 需要设计可靠上报协议和中心事实模型 |

采用第三种方案。

### 4.2 OpenCost 集成决策

不导入 OpenCost 应用运行时，也不暴露其 Go 类型。新增 Crane 自有的
`AllocationEngine` 接口和规范化输入输出模型。`pkg/cost/allocation/opencost`
实现经过裁剪的兼容引擎：

- 以 OpenCost `v1.120.4` 的 Allocation、Asset、Idle 和 Shared Cost 行为为
  初始兼容基线。
- 只迁移中心计算需要的算法，不迁移 HTTP Server、云集成、缓存和 UI。
- 迁移代码保留 Apache-2.0 许可证、NOTICE 和上游 commit 来源。
- Crane 公共 API、数据库和 gRPC 协议只使用 Crane 自有类型。
- 通过固定输入的 Golden Fixtures 对比上游语义；升级基线必须显式评审差异。

该边界既获得 OpenCost 成本模型能力，又避免运行多个成本服务和形成不稳定的
源码级公共依赖。

## 5. 总体架构

```text
业务 Kubernetes 集群
  crane-agent DaemonSet
    |- 节点/cgroup/CRI 指标
    |- QoS 与节点级动作
    `- /metrics -> 本地 Prometheus

  craned Deployment（Leader）
    |- Kubernetes Inventory Informer
    |- 本地 Prometheus 区间查询与 Usage 汇总
    |- Recommendation/EHPA/EVPA Controllers
    |- Cluster Reporter / Plan Executor
    `- 出站 mTLS gRPC Stream
                    |
                    v
中心 crane-server（2+ 副本）
    |- Cluster Registry / Session Gateway
    |- Inventory & Usage Ingestion
    |- OpenCost-compatible Allocation Engine
    |- Cloud Billing / Rate Card Providers
    |- Reconciliation / Recommendation / Governance
    |- REST API / Dashboard 静态资源
    `- MySQL 8 + 可选 S3 兼容原始对象归档
```

### 5.1 集群侧组件

集群继续只运行 Crane 原有核心组件和其现有指标依赖：

- `crane-agent` 保持 DaemonSet 形态，只读取本节点 Pod、Node、CRI 和 cgroup。
  它不建立中心连接，也不扫描全集群对象。
- `craned` 增加 Cluster Reporter 和 Plan Executor。Reporter 使用 Leader Election，
  一个集群同一时刻只有一个有效上报者。
- `craned` 从 Kubernetes Informer 获取资源关系，从本地 Prometheus 查询区间用量，
  生成紧凑、可重放的窗口事实。
- Plan Executor 将中心的类型化计划转换为本地 Recommendation、EHPA 或 EVPA，
  并由现有本地控制器执行。
- Metric Adapter、Prometheus 和现有控制器仍按当前 Crane 功能需要部署；本文不新增
  OpenCost Exporter、OpenCost Server、消息队列或数据库。

### 5.2 中心 `crane-server`

`crane-server` 是模块化单体而不是多个独立微服务：

- 接收 Agent 注册、长连接、上报批次和执行状态。
- 管理租户、集群、云账号、成本中心和权限。
- 拉取多云账单与费率，标准化为精确成本事实。
- 运行分摊、聚合、对账、异常检测和推荐任务。
- 保存优化计划，管理审批并通过双向流下发 Desired State。
- 对 Dashboard 暴露统一 REST API，并托管构建后的静态资源。

内部模块通过 Go 接口和领域模型隔离。部署上保持一个 Server 二进制，生产运行两个
或更多副本；后台任务通过 MySQL 租约保证单任务单 Worker。

### 5.3 信任边界

中心不持有业务集群 kubeconfig。所有连接均由集群 `craned` 主动发起，适应企业
防火墙和 NAT。中心只产生期望计划；本地 Agent 根据本地 RBAC、资源版本和安全
条件决定是否创建或更新 Crane CRD。

本文后续协议中的“Agent”特指 `craned` 内的集群连接角色；逐节点 DaemonSet 始终
称为 `crane-agent`，它不直接连接中心。

## 6. C/S 协议

新增版本化 `finops.crane.io/v1` Protobuf 协议。协议与现有单次查询的 History gRPC
Provider 分离，避免把不安全的临时接口扩展成控制通道。

### 6.1 注册与连接

1. 管理员在中心创建集群，获得一次性、短期 Bootstrap Token。
2. `craned` 使用 Token 调用 `RegisterCluster`，提交 Cluster UID、版本和能力。
3. 中心验证 Token 后签发绑定 `tenant_id` 和 `cluster_id` 的短期客户端证书。
4. Agent 使用证书建立 `Connect` 双向 mTLS Stream。
5. Agent 定期自动轮换证书；Bootstrap Token 使用一次后立即失效。

### 6.2 上行消息

- `ClusterHello`：协议版本、Agent 版本、Kubernetes 版本、能力和最后 ACK。
- `InventoryDelta`：Node、Namespace、Controller、Workload、Pod、Container、
  Service、PV/PVC、OwnerReference 和允许的业务标签变化。
- `UsageWindow`：固定 `[start, end)` 窗口内的 CPU core-seconds、内存 byte-seconds、
  GPU-seconds、PV byte-seconds、网络字节、request/limit 和节点运行时长。
- `ExecutionStatus`：计划版本、本地 CRD、动作、SLO、回滚和副本变化。
- `Heartbeat`：能力、时钟偏差、队列水位、Prometheus freshness 和健康信息。

每批消息包含 `tenant_id`、`cluster_id`、`epoch`、`sequence`、窗口、schema 版本和
校验和。传输使用压缩和大小上限，不上传原始 Prometheus 样本。

### 6.3 下行消息

- `DesiredState`：当前租户/集群应持有的计划版本集合。
- `OptimizationPlan`：类型化资源、Replica、EHPA、EVPA 或节点优化建议。
- `CancelPlan`：撤销未执行计划或请求恢复保存的安全基线。
- `ServerAck`：已持久化的上行高水位和被隔离批次。
- `RotateCertificate`：证书轮换窗口与新凭证。

### 6.4 可靠性语义

- 上报采用 at-least-once；中心按集群、epoch、sequence 和窗口幂等写入。
- 中心只有在数据库事务提交后才返回 ACK。
- Agent 重连任意 Server 副本后，从最后持久化 ACK 续传。
- 短时失败保留内存队列；长时失败从本地 Prometheus 保留期重新构建 UsageWindow。
- 中心 Desired State 存储在 MySQL；Agent 重连后比较完整状态，而不是依赖易丢事件。
- 超过乱序窗口的数据进入回补批次，不能覆盖更新版本。

## 7. 采集与规范化

### 7.1 Inventory

`craned` 使用 Shared Informer 维护集群清单，按 ResourceVersion 发送增量，并周期性
发送校验快照。资源身份使用 Kubernetes UID，不使用易复用的名称作为唯一键。

工作负载归属沿 OwnerReference 向上解析到 Deployment、StatefulSet、DaemonSet、
Job、CronJob 或可扩展顶层 Controller。解析失败的对象保留为 `unallocated`，不能
静默丢弃。

标签由中心下发白名单，只上报成本中心、团队、环境、产品等允许键。系统标签和
可能包含个人信息的标签默认拒绝。

### 7.2 UsageWindow

默认每 5 分钟构建窗口，中心生成小时事实。窗口至少包含：

- CPU usage 与 request 的 core-seconds。
- Memory working set 与 request 的 byte-seconds。
- GPU allocation/usage seconds；不支持遥测时显式标记缺失。
- PV provisioned/used byte-seconds。
- 按云厂商计费类别可识别的网络 ingress/egress bytes。
- Node capacity、allocatable、uptime、provider ID、region、zone 和 instance type。

聚合必须使用区间积分而不是单点乘时长。数据缺口、重启和计数器回绕需要带上
coverage，中心不能把缺失数据视为零使用量。

### 7.3 数据新鲜度

Agent 报告 Inventory、Usage、Recommendation 和 SLO 四种独立 freshness。历史
成本仍可查询，但任何一个自动计划所需数据超过两个上报窗口未更新时，该计划不得
进入 Auto。

## 8. 成本模型与 OpenCost 语义

### 8.1 成本事实分层

三类事实物理隔离：

- `usage_facts_hourly`：无金额的 Kubernetes 使用量。
- `estimated_cost_hourly`：费率乘使用量得到的小时估算成本。
- `billing_line_items`：T+1/T+2 云厂商实付账单，包括 list、net 和 amortized。

API 必须显式返回 `cost_kind=estimated|billed` 和
`cost_metric=list|net|amortized`。估算与实付可对比但不能直接相加。

### 8.2 Asset

Asset 至少覆盖 Node、LoadBalancer、PV、网络、控制面和中心定义的 Shared Asset。
Node 与 PV 通过 Provider ID、账号、region、zone 和资源 ID 映射云账单；无法映射的
资产保留原因和金额。

### 8.3 Allocation

Allocation 支持以下维度：

- cluster、node、namespace、controller、workload、service。
- pod、container、label、annotation 白名单和 cost center。
- CPU、memory、GPU、PV、network、load balancer、control plane 和 shared。

计算同时保留 request 成本和 usage 成本。默认按资源 request 分配已占用容量，
usage 用于效率分析；策略可选择按 usage、request 或最大值分摊，但结果必须标记模式。

### 8.4 Idle 与 Shared Cost

节点总成本减去已分配资源成本得到 Idle。Shared Cost 可按 CPU、内存、直接成本或
自定义固定权重分配。每个窗口必须满足：

```text
Asset Cost = Allocated Cost + Idle Cost + Unallocated Cost
```

财务金额使用现有 `model.Decimal`，禁止以 `float64` 持久化或计算。负账单、退款和
修订保持原始符号。

### 8.5 多云账单

现有阿里云、腾讯云、华为云和火山引擎 Provider 迁入中心模块；随后增加 AWS、
Azure 和 GCP。Provider 只负责鉴权、分页、原始响应和标准化，不承担调度、事务、
汇率和分摊。

原币种永不覆盖。跨币种聚合必须有带日期和来源的汇率；缺少汇率时拒绝聚合。
账单补录和退款更新当前事实并保存修订历史。

## 9. 中心数据模型

MySQL 8 是首期唯一必需的中心数据依赖。

| 表 | 用途 |
|---|---|
| `tenants` | 企业租户和默认治理设置 |
| `clusters` | 集群身份、能力、版本和状态 |
| `agent_sessions` | 证书、epoch、ACK 和 freshness |
| `inventory_current` | 当前 Kubernetes 资源维度 |
| `inventory_changes` | UID 与 ResourceVersion 变更历史 |
| `usage_facts_hourly` | 无金额的小时使用量 |
| `cloud_accounts` | Provider 配置和凭证引用 |
| `collection_runs` | 账单任务、游标、租约和错误 |
| `rate_cards` | 目录价、合同价和有效期 |
| `billing_line_items` | 实付账单事实 |
| `billing_line_item_revisions` | 补账、退款和修订历史 |
| `resource_mappings` | 云资源、集群、工作负载和成本中心映射 |
| `estimated_cost_hourly` | 小时估算成本 |
| `cost_allocations_hourly` | 多维分摊事实 |
| `cost_aggregates_daily` | 常用日级聚合 |
| `reconciliation_runs` | 估算与实付覆盖率和差异 |
| `optimization_plans` | 中心优化计划和审批状态 |
| `optimization_actions` | 集群执行、回滚和结果 |
| `realized_savings` | 基线、实际成本和已实现节省 |
| `budgets`、`anomalies` | 企业预算与异常事件 |
| `audit_events` | 管理、审批、映射和重放审计 |

所有租户数据表包含 `tenant_id`，集群事实还包含 `cluster_id`。事实表按月分区。
高频上报处理后保留 7 天用于重放；小时事实保留 24 个月；日/月聚合长期保留。
Store/Repository 方法必须显式接收租户身份，并在 SQL 条件中包含 `tenant_id`；
不能只依赖 HTTP Handler 的过滤。

S3 兼容对象存储不是 Phase 1 C/S 基础的依赖，但在 Phase 3 云账单进入生产前必须
配置，用于归档原始响应。对象存储不可用时不得发布无法追溯的账单批次。开发环境
可使用文件归档。

## 10. 优化计划与集群执行

### 10.1 OptimizationPlan

中心下发类型化计划，不下发任意 JSON Patch。计划包含：

- `plan_id`、revision、租户、集群和推荐类型。
- TargetRef 的 API Version、Kind、Namespace、Name、UID 和预期 ResourceVersion。
- 当前基线、推荐值、预计月节省、置信度、风险和证据窗口。
- SLO、PDB、最小副本、资源上下限、冷却时间和变更幅度限制。
- 生效时间、过期时间、执行模式、审批信息和回滚基线。
- 中心签名和协议版本。

支持资源 Request/Limit、Replica、EHPA min/max、预测参数、Cron、EVPA 和空闲节点
候选。节点删除、实例变更等云基础设施动作不在首批自动执行范围。

### 10.2 执行状态机

```text
Draft -> Observing -> Preview -> AwaitingApproval -> Approved
      -> Applying -> Verifying -> Succeeded
                           |-> Failed -> RollingBack -> RolledBack
      -> Expired / Cancelled / Rejected
```

自动化成熟度固定为：

```text
Observe -> Preview -> Approval -> Auto Allowlist
```

新策略默认 Preview。只有租户策略、目标白名单、风险阈值和数据 freshness 同时满足
时才允许 Auto。

### 10.3 本地执行

Plan Executor 在集群内依次验证：

1. 签名、租户、集群、版本、过期时间和幂等记录。
2. 目标 UID 和 ResourceVersion，拒绝名称复用或并发配置变化。
3. Namespace、资源类型和动作是否在本地允许范围。
4. PDB、最小副本、资源上下限、SLO 和冷却时间。
5. 当前 Crane CRD 是否由该计划管理，避免覆盖人工配置。

验证通过后转换为 Recommendation、EHPA 或 EVPA。EHPA 新建时使用 Preview；切换
Auto 必须是独立审批动作。已有 EHPA 的用户 ScaleStrategy 和 Prediction 不被中心
静默覆盖。

### 10.4 回滚与节省验收

执行前保存目标基线。验证期间若错误率、延迟、不可用 Pod、资源压力或扩缩容振荡
超过阈值，本地立即停止或回滚，无需等待中心在线。

中心比较执行前基线、模型预测和执行后实际成本，保存已实现节省、未实现节省、
回滚损失以及 SLO 变化。预计节省不能直接计入财务收益。

## 11. 外部 API 与 Dashboard

中心 REST API 以租户和集群权限过滤，至少包括：

- `/api/v1/clusters`：注册、能力、版本、连接和 freshness。
- `/api/v1/cost/allocations`：Kubernetes 多维分摊。
- `/api/v1/cost/assets`：Node、PV、LB、网络和控制面资产。
- `/api/v1/cost/cloud`：实付账单和多云聚合。
- `/api/v1/cost/reconciliation`：估算/实付覆盖率和差异。
- `/api/v1/recommendations`：成本优化建议和证据。
- `/api/v1/optimization-plans`：审批、执行、回滚和节省验收。
- `/api/v1/budgets`、`/api/v1/anomalies`：预算和异常治理。
- `/api/v1/audit-events`：不可变操作审计。

大范围明细使用异步导出。所有成本响应返回时间窗口、币种、cost kind、cost metric、
coverage、freshness、分摊模式和来源版本。

Dashboard 只访问 `crane-server`。页面按成本概览、Allocation、Asset、账单对账、
优化计划、预算异常和平台健康组织，不直接查询 MySQL 或业务集群。

## 12. 安全设计

- gRPC 使用双向 TLS；客户端证书 SAN 绑定租户和集群。
- Bootstrap Token 一次性使用、短期有效并只允许注册指定集群。
- 云凭证只保存 Secret/Vault 引用，每次任务重新读取以支持轮换。
- 指令包含签名、plan ID、revision、nonce、前置条件和过期时间，防止重放与降级。
- 标签使用租户白名单，限制键、值长度和每对象数量。
- 中心 RBAC 包括 Viewer、FinOps Analyst、Operator、Approver 和 Admin。
- 普通用户只能读取授权成本中心和集群；审批者不能修改系统签名或执行结果。
- 数据库迁移、服务读写和报表只读使用不同账号。
- 日志、指标、错误和审计事件禁止包含凭证明文、完整账单响应和敏感标签值。
- 管理、审批、回滚、资源映射、汇率和重放操作全部记录审计。

## 13. 容错与降级

- `crane-server` 运行两个或更多副本，API 无状态，任务使用 MySQL 租约。
- `craned` Reporter 使用 Kubernetes Leader Election；Leader 变化产生新 epoch。
- 网络失败指数退避并加入随机抖动，避免多集群同时重连。
- 中心按幂等键去重；一个集群、云账号或账期失败不阻塞其他对象。
- Schema Drift 或非法数据归档并隔离，不进入正式事实和聚合。
- 中心断联时，本地 EHPA/HPA、QoS 和最后安全策略继续运行；不执行新的中心计划。
- Agent 断线在本地 Prometheus 保留期内可回补，超出保留期明确显示 coverage 缺口。
- MySQL 短暂不可用时不 ACK 上报、不发布账单批次、不丢弃 Desired State。
- 对象存储不可用时云账单采集任务重试，已发布事实保持可读。
- 数据 stale 时历史报表仍可读，但自动优化被关闭并产生告警。

## 14. 可观测性

中心指标按租户和集群使用受控标签，避免资源级高基数：

- 连接数、注册失败、证书到期和协议版本。
- 每类批次延迟、大小、ACK 水位、重复、乱序、隔离和回补。
- Inventory/Usage/SLO freshness 与 coverage。
- 分摊任务时长、平衡失败、unallocated 比例和账单差异。
- Plan 各状态数量、执行时长、拒绝原因、回滚和 SLO 变化。
- Provider 限流、鉴权失败、schema drift 和账期完成度。

Tracing 只覆盖批次 ID、plan ID 和任务 ID，不记录成本明细或业务标签值。

## 15. 测试设计

### 15.1 测试层次

1. 单元测试：Decimal、Inventory 归属、窗口积分、Allocation、Idle/Shared、评分和
   保护条件。
2. OpenCost 兼容测试：固定资源、用量和价格输入，通过 Golden Fixtures 验证
   Allocation、Asset 和 Idle 语义。
3. Provider 契约测试：签名、分页、负金额、退款、币种、修订和错误分类。
4. 协议测试：N/N-1、未知字段、重复、乱序、断点续传、压缩和消息上限。
5. 多租户安全测试：身份伪造、跨租户读取、过期证书、指令重放和标签泄漏。
6. 集成测试：两个 Kind 业务集群、双副本 Server 和真实 MySQL 完成全链路。
7. 故障测试：Agent/Server/MySQL 重启、网络分区、证书轮换、限流和部分账单。
8. 控制安全测试：旧 ResourceVersion、PDB、最小副本、SLO、冷却、重复执行和回滚。
9. 升级测试：中心先升级、Agent 先升级和滚动升级均维持 N/N-1 可用。

PR CI 不访问真实云账号。Provider 使用脱敏 Fixtures；受控环境执行周期性真实账号
冒烟和月度金额验收。

### 15.2 验收不变量

- `Asset Cost = Allocated + Idle + Unallocated`，精确金额无浮点误差。
- 重复上报和批次重放不产生重复事实或重复动作。
- 月度实付汇总与云厂商汇总差异不超过币种最小单位。
- 断线恢复可回补本地 Prometheus 保留期内数据并报告无法回补窗口。
- 数据超过两个上报窗口未更新时，不生成自动执行计划。
- 同一 plan ID 和 revision 最多执行一次。
- 保护条件触发后 5 分钟内停止动作或恢复安全基线。
- 所有查询和动作强制租户边界，跨租户测试全部拒绝。

### 15.3 容量与性能

- 50 个 Kubernetes 集群、20 个云账号。
- 每月 1,000 万条账单明细，在线保留 24 个月。
- 日级聚合查询 p95 小于 1 秒。
- 200 条明细分页查询 p95 小于 2 秒。
- 两副本并发运行不产生重复批次、成本或计划。
- Server 进程退出后 60 秒内恢复任务领取；Agent 重连使用抖动避免惊群。

## 16. 迁移设计

1. 保留现有 `craned`、`crane-agent`、Recommendation 和 EHPA 控制器；新能力通过
   Feature Gate 开启。
2. 将 `cost-collector` 的 Provider、精确金额和账单模型迁入 `crane-server` 内部模块。
3. 旧 `/api/v1/providers`、`/api/v1/costs`、`/api/v1/costs/summary`、
   `/api/v1/rates` 和 `/api/v1/allocate` 保留一个兼容发布周期。
4. 当前 URL Cluster Store 进入只读兼容，集群逐步改为 Agent 注册。
5. 新旧用量和成本路径双写校验；Allocation 与账单对账通过后切换 Dashboard。
6. Plan Executor 稳定后，托管集群关闭 Dashboard 直接 Patch 的采纳路径。
7. 文件账本只保留开发模式，生产数据迁移到 MySQL 后停止写入。

迁移不得要求业务工作负载停机。中心或 Agent Feature Gate 回退时，本地 Crane 控制器
继续工作，尚未执行的中心计划保持暂停。

## 17. 分阶段交付

### Phase 1：C/S 基础

- `crane-server` 模块化单体骨架和 MySQL 迁移。
- Cluster Registry、Bootstrap、mTLS、证书轮换和双向 Stream。
- `craned` Reporter/Executor Feature Gate、Leader Election 和可靠 ACK。
- Inventory、Usage、Session 和 Desired State 基础存储。
- 两个 Kind 集群的断线、重连、去重和多租户集成测试。

### Phase 2：OpenCost 兼容成本能力

- Crane `AllocationEngine` 和 OpenCost Golden Fixtures。
- Inventory 归属、UsageWindow、CPU、内存、GPU、PV 和网络事实。
- Asset、Allocation、Idle、Shared Cost 和多维查询。
- 现有成本 Dashboard 迁移到中心 API。

### Phase 3：多云账单与对账

- 国内四家 Provider 迁入中心并生产化。
- AWS、Azure、GCP 账单和费率。
- 资源映射、多币种、汇率、修订、对象归档和估算/实付对账。

### Phase 4：优化闭环

- `OptimizationPlan`、审批、签名、幂等、过期和回滚。
- Recommendation、EHPA、EVPA 与成本收益排序。
- Observe、Preview、Approval、Auto Allowlist 分级开放。
- SLO 验证和已实现节省计算。

### Phase 5：企业治理

- 预算、预测、异常检测、告警和 Showback/Chargeback。
- 成本中心、RBAC、审计、异步导出和统一 Dashboard。
- 平台健康、数据质量和 Provider 覆盖率视图。

### Phase 6：生产加固

- HA、备份恢复、容量与长期稳定性验证。
- 协议滚动升级、证书生命周期和安全评估。
- 运行手册、灾难恢复演练和升级/回滚发布门槛。

## 18. 目标代码边界

```text
cmd/crane-server/                         中心服务入口
pkg/clusteragent/reporter/                craned Inventory/Usage 上报
pkg/clusteragent/executor/                本地计划验证和 CRD 转换
pkg/finops/proto/v1/                      C/S Protobuf 协议
pkg/finops/gateway/                       注册、会话、ACK 和 Desired State
pkg/finops/inventory/                     资源身份与归属
pkg/finops/usage/                         UsageWindow 构建和规范化
pkg/cost/model/                           Crane 成本领域模型
pkg/cost/allocation/opencost/             OpenCost 兼容算法
pkg/cost/provider/                        多云账单与费率 SPI
pkg/cost/store/mysql/                     中心 MySQL 实现
pkg/optimization/                         评分、计划、审批和节省验收
pkg/server/handler/                       中心 REST API
pkg/web/                                  统一 Dashboard
```

这些目录是所有权边界，不要求在一个阶段全部创建。Phase 1 计划只能涉及 C/S 基础
所需部分。

## 19. 关键约束

- 集群内不部署 OpenCost 运行时或新的持久化服务。
- 中心不保存 kubeconfig，不直接 Patch 业务集群。
- OpenCost 内部类型不进入 Crane 公共协议、API 或数据库。
- 原始高频时序留在集群，中心接收带 coverage 的窗口事实。
- 财务金额使用 Decimal，estimated 与 billed 物理隔离。
- 成本影响优先级和策略参数，但本地 SLO 与安全条件拥有最终否决权。
- 新策略默认 Preview，Auto 只对显式白名单开放。
- 无法映射、缺失和过期数据必须可见，不能按零值或成功状态处理。
- 所有跨租户数据访问和执行都必须在存储、服务和协议三层验证。

该架构使 Crane 成为统一的企业 FinOps 产品和优化控制面，同时保持集群侧部署精简、
故障隔离和本地自治。
