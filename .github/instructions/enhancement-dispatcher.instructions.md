---
description: 当涉及 Dispatcher 架构优化、节点分区再平衡、调度器负载均衡、Pod 分发策略、或新增/修改 pkg/dispatcher 中相关代码时，加载此指令
applyTo: '**/dispatcher/**/*.go'
---

# Gödel Dispatcher 增强改造指令

## 1. 当前架构分析

### 1.1 Dispatcher 在三级流水线中的角色

```
API Server (Pod Watch)
        │
        ▼
   ┌──────────┐     ┌──────────────┐     ┌───────────┐
   │Dispatcher│────▶│Scheduler A+B │────▶│API Server │
   │ (1 实例)  │     │(Node Partition)│     │(Bind Pod) │
   └──────────┘     └──────────────┘     └───────────┘
        │
        ├── Policy Manager (TODO: 未实现，仅占位)
        ├── Node Shuffler (节点分区再平衡)
        ├── Scheduler Maintainer (调度器生命周期管理)
        └── Pod State Reconciler (异常状态 Pod 修复)
```

Dispatcher 是 Gödel 流水线的入口，负责：
1. **Pod 分发**：将 Pending Pod 分配到某个 Scheduler 实例
2. **节点分区管理**：通过 Node Shuffler 维护 Scheduler ↔ Node 的分区映射
3. **调度器生命周期**：通过 Scheduler Maintainer 监控活跃/非活跃调度器
4. **状态修复**：通过 Reconciler 修复异常状态 Pod（如调度器宕机后的孤儿 Pod）

### 1.2 子模块详细分析

#### 1.2.1 Policy Manager（排序策略管理器）

**文件**: `pkg/dispatcher/policy/manager.go`

**当前状态**: **完全未实现**，仅有 TODO 注释：
```go
// TODO: Implement a better PolicyManager
// The PolicyManager will manage all quota queues, applications, and pods,
// which will follow specific sorting policies (DRF/FairShare/...) during scheduling.
```

**相关接口定义**: `pkg/dispatcher/internal/store/schedulable.go` 已定义 `Schedulable` 接口（含 DRF 相关方法: `GetDemand`, `GetResourceUsage`, `GetMinShare`, `GetMaxShare`, `GetFairShare`, `SetFairShare`），但无任何实现。

**影响**: Pod 排序完全依赖 FIFO，无法实现公平调度或优先级抢占。

#### 1.2.2 Node Shuffler（节点分区再平衡）

**文件**: `pkg/dispatcher/node-shuffler/`

**职责**: 维护 Scheduler ↔ Node 的分区映射，确保节点均匀分配到各活跃调度器。

**核心流程**:
```
事件驱动入队:
  Node Add/Update → addNodeToProcessingQueueIfNecessary()
    - 无调度器注解 → 入队 (NoSchedulerName)
    - 注解指向非活跃/不存在的调度器 → 入队 (InactiveScheduler)

工作线程 (每秒):
  nodeProcessingWorker() → Get() → updateSchedulerNameForNode()
    → chooseOneSchedulerForThisNode() → "选节点最少的调度器"
    → updateNodeSchedulerNameAnnotation() → API Server Patch

再平衡 (每分钟):
  ReBalanceSchedulerNodes()
    → GetSchedulersWithMostAndLeastNumberOfNodes()
    → if most > least * 2 → 移动 (most+least)/2 - least 个节点
```

**关键数据结构**:
- `nodeQueue`: 基于 `workqueue.Type` + `nodeCache map[string]bool` 的去重队列
- `NodeToBeProcessed`: 包含 `nodeName` + `reason` (NoSchedulerName/InactiveScheduler/TooManyNodesInThisPartition)

#### 1.2.3 Scheduler Maintainer（调度器生命周期管理器）

**文件**: `pkg/dispatcher/scheduler-maintainer/`

**职责**: 管理 Scheduler CRD 实例的活跃/非活跃状态。

**核心数据**:
```go
type SchedulerMaintainer struct {
    schedulerMux      sync.RWMutex
    generalSchedulers map[string]*GodelScheduler  // 所有调度器（含活跃/非活跃）
}
```

**活性检测**: `IsSchedulerActive()` — 检查 `scheduler.Status.LastUpdateTime`，超过 2 分钟未更新视为非活跃。

**定时任务**:
- `PopulateSchedulers()` (每 1 分钟): 从 Lister 拉取所有 Scheduler CRD 同步到缓存
- `SyncUpSchedulersStatus()` (每 30 秒): 检查活跃/非活跃状态，删除持续非活跃的 Scheduler CRD

#### 1.2.4 Dispatch Info（调度器负载均衡存储）

**文件**: `pkg/dispatcher/internal/store/dispatch.go`

**职责**: 跟踪每个调度器的已分发 Pod 数量，实现负载均衡选择。

**核心方法**:
```go
GetMostIdleSchedulerAndAddPodInAdvance(pod) string
  → 遍历所有调度器，找 SchedulerToPods 数量最少的
  → 相同数量时使用 Reservoir Sampling 随机选一个
```

#### 1.2.5 Unit Infos（Gang/PodGroup 管理）

**文件**: `pkg/dispatcher/internal/queue/unit.go`

**职责**: 管理 PodGroup 的就绪状态，当 Pod 数量达到 MinMember 后批量释放到分发队列。

**定时检查**: `populate()` 每 30 秒扫描所有 unit，检查 `readyToBeDispatched()`。

### 1.3 Pod 分发主流程

```
Pod Watch (Pending) → addPodToPendingOrSortedQueue()
    │
    ├── PodGroup Pod → UnitInfos.AddUnSortedPodInfo()
    │       │
    │       └── readyToBeDispatched()? → movePodsToReadyQueue() → readyUnitPods FIFO
    │                                                                     │
    │                                                                     ▼
    │                                                          pendingUnitPodsLoop()
    │                                                                     │
    └── Non-PodGroup Pod ─────────────────────────────────────────────────┤
                                                                          ▼
                                                              FIFOPendingPodsQueue
                                                                          │
                                                                          ▼
                                                                   pendingLoop()
                                                                          │
                                                                          ▼
                                                              SortedPodsQueue (FIFO)
                                                                          │
                                                                          ▼
                                                                   sortedLoop()
                                                                          │
                                                              go dispatchingPod()
                                                                          │
                                                               selectScheduler()
                                                                          │
                                                        ┌─────────────────┴──────────────────┐
                                                        │                                    │
                                                  PodGroup Pod                        Non-PodGroup Pod
                                                        │                                    │
                                              selectSchedulerForUnit()              loadBalancing()
                                                (缓存/复用已选调度器)             (GetMostIdleScheduler)
                                                        │                                    │
                                                        └──────────────┬─────────────────────┘
                                                                       │
                                                              sendPodToScheduler()
                                                                       │
                                                              PatchPod (annotation)
                                                                       │
                                                   Scheduler Informer picks up
```

---

## 2. 当前实现的性能与稳定性问题

### 问题 P1：节点再平衡策略粗糙 — 仅按节点数量均分，忽略资源异构性

**位置**: `node-shuffler/shuffler_util.go` + `node-shuffler/node_shuffler.go`

**现象**:
- `chooseOneSchedulerForThisNode()` 只选"节点最少的调度器"
- `ReBalanceSchedulerNodes()` 均衡条件 `most > least * 2`，移动量 `(most+least)/2 - least`
- 完全忽略节点资源大小（例如 128 核大节点 vs 4 核小节点在计数上等价）

**后果**:
- 调度器 A 分配到 100 个 4 核节点（400 核），调度器 B 分配到 100 个 128 核节点（12800 核）
- 负载完全不均衡，调度器 A 的 Pod 频繁调度失败

**代码证据**:
```go
// node_shuffler.go:283
if schedulerInfo.MostNumberOfNodes > schedulerInfo.LeastNumberOfNodes*2 &&
    schedulerInfo.MostNumberOfNodes > 1 {
    numberOfNodesNeedToBeMoved := (schedulerInfo.MostNumberOfNodes+schedulerInfo.LeastNumberOfNodes)/2 -
        schedulerInfo.LeastNumberOfNodes
```

### 问题 P2：调度器选择无资源感知 — 分发时忽略 Pod 资源需求和调度器容量

**位置**: `internal/store/dispatch.go` `GetMostIdleSchedulerAndAddPodInAdvance()`

**现象**:
- 负载均衡仅按"已分发 Pod 数量"最少做选择
- 一个需要 64 核的 Pod 和一个需要 100m 的 Pod 在均衡时权重相同
- 不检查目标调度器的分区是否有足够资源容纳 Pod

**后果**:
- 大资源 Pod 被分发到只有小节点的调度器 → 必然调度失败 → 回退到 Dispatcher 重分发
- 增加无效的调度尝试，浪费端到端延迟

**代码证据**:
```go
// dispatch.go:257
for schedulerName := range dq.Schedulers {
    cnt := 0
    if dq.SchedulerToPods[schedulerName] != nil {
        cnt = dq.SchedulerToPods[schedulerName].Len()  // 仅按 Pod 数量
    }
    if cnt < max { ... }
}
```

### 问题 P3：全局粗粒度锁 — SchedulerMaintainer 单锁保护所有操作

**位置**: `scheduler-maintainer/scheduler_maintainer.go` + `maintainer_util.go`

**现象**:
- `generalSchedulers map` 被单一 `schedulerMux sync.RWMutex` 保护
- 读操作（`GetActiveSchedulers`, `IsSchedulerInActiveQueue`, `GetNumberOfNodesFromActiveScheduler` 等）和写操作（`AddScheduler`, `AddNodeToGodelScheduler` 等）共享同一把锁
- Node 事件（Add/Update/Delete）每次都需要获取写锁
- 高频率 Node Update 事件在大集群中造成锁争用

**后果**:
- 5000 节点集群中，Node Update 事件频率可达数百/秒
- 每次 `UpdateNodeInGodelSchedulerIfNecessary()` 获取写锁，阻塞所有读操作
- `selectScheduler()` → `loadBalancing()` → `GetMostIdleSchedulerAndAddPodInAdvance()` 需要读 `DispatchInfo`，而 `DispatchInfo.AddScheduler/DeleteScheduler` 需要写锁，形成竞争

### 问题 P4：Re-Balance 周期固定且无反馈机制

**位置**: `node-shuffler/node_shuffler.go` `Run()` + `ReBalanceSchedulerNodes()`

**现象**:
- Re-Balance 固定每 1 分钟执行一次（`go wait.Until(ns.ReBalanceSchedulerNodes, time.Minute, stopCh)`）
- 不考虑当前系统负载、是否有调度失败、是否有新调度器加入等信号
- 移动节点涉及 API Server Patch，每个节点一次 `Nodes().Update()`，无批量化

**后果**:
- 突发场景（如新调度器加入）响应延迟最多 1 分钟
- 稳定期也每分钟执行全量检查，浪费 CPU
- 大规模移动时（如 100 个节点），逐个 API 调用，耗时可达数十秒

### 问题 P5：Policy Manager 完全未实现 — FIFO 分发无公平性保障

**位置**: `pkg/dispatcher/policy/manager.go`（空文件）

**现象**:
- 所有 Pod FIFO 排队，无优先级区分、无公平共享
- `Schedulable` 接口定义了 DRF 相关方法但无实现
- SortedPodsQueue 实际是 FIFO，命名误导

**后果**:
- 无法按 Namespace/队列实施公平调度
- 突发大任务可饿死小任务
- 无法实现多租户资源隔离

### 问题 P6：Pod 分发是 goroutine-per-pod 无限扩散

**位置**: `dispatcher.go` `sortedLoop()` → `go d.dispatchingPod(...)`

**现象**:
```go
// dispatcher.go:300
go d.dispatchingPod(ctx, podInfo, d.SortedPodsQueue)
```
- 每个 Pod 分发启动一个 goroutine
- 无限制并发，突发 10000 Pod 时同时创建 10000 goroutine
- 每个 goroutine 执行 `PatchPod`（API Server 写操作）

**后果**:
- API Server 写入风暴，可达 QPS 限制被 throttled
- goroutine 数量不可控，内存压力
- 代码中 TODO 已标注：`// TODO(zhangrenyu): make sure it won't impact the performance if remove goroutine`

### 问题 P7：Node/NMNode 双重 API 调用 — 分区更新非原子性

**位置**: `node-shuffler/node_shuffler.go` `updateNodeSchedulerNameAnnotation()`

**现象**:
- 分区变更时需要分别更新 Node 和 NMNode 两种资源
- 两次 API 调用非原子性: 第一次成功后第二次可能失败
- 失败后无重试机制，`SyncUpNodeAndCNR()` 是空实现：`// TODO: sync up scheduler name in node and cnr`

**后果**:
- Node 和 NMNode 的 `scheduler-name` 注解可能不一致
- 调度器看到不一致状态，导致节点调度异常

---

## 3. 创新方案设计

### 创新点 1：资源感知的动态节点分区（Resource-Aware Dynamic Node Partitioning）

**解决问题**: P1（粗糙均衡）+ P4（固定周期）

**核心思想**: 将节点分区的均衡目标从"节点数量相等"改为"可分配资源容量加权相等"，并引入反馈驱动的自适应再平衡。

**设计**:

```
                    ┌──────────────────────────────┐
                    │   Resource-Aware Partitioner │
                    ├──────────────────────────────┤
                    │ 1. 节点权重 = Σ(资源维度加权) │
                    │    w(n) = α·cpu + β·mem +    │
                    │           γ·gpu + ...         │
                    │ 2. 分区目标: Σw(partition_i)  │
                    │    ≈ Σw(partition_j) ∀i,j    │
                    │ 3. 自适应触发:                │
                    │    - 不均衡度超阈值           │
                    │    - 调度失败率上升           │
                    │    - 调度器上下线             │
                    └──────────────────────────────┘
```

**关键技术点**:
- **多维资源权重**: `NodeWeight(n) = α·Allocatable.CPU + β·Allocatable.Memory + γ·Allocatable.GPU`
- **不均衡度指标**: `Imbalance = max(W_i) / min(W_j) - 1`，超阈值（如 0.2）才触发
- **资源感知迁移**: 移动节点时选择最小化不均衡度的节点，而非随机
- **反馈信号**: 调度失败率 `scheduler_schedule_unit_result{result=failure}` 作为触发条件
- **迁移成本评估**: 节点上已有 Pod 数量越多，迁移优先级越低

**学术价值**: 将经典的"加权负载均衡"理论应用到 Kubernetes 节点分区场景，证明资源感知分区可将跨分区调度失败率降低 X%。

### 创新点 2：拓扑感知的调度器选择（Topology-Aware Scheduler Selection）

**解决问题**: P2（无资源感知分发）

**核心思想**: Dispatcher 在选择调度器时，基于 Pod 资源需求的"特征向量"和调度器分区资源"画像"进行匹配，替代简单的 Pod 计数均衡。

**设计**:

```
Pod 到达 Dispatcher
       │
       ▼
┌──────────────────────┐
│ 1. 提取 Pod 资源指纹  │  → (cpu_req, mem_req, gpu_req, ...)
│ 2. 查询分区资源画像    │  → per-scheduler: (avail_cpu, avail_mem, avail_gpu, ...)
│ 3. 可行性过滤          │  → 排除资源不足的调度器
│ 4. 亲和力评分          │  → score(s) = 匹配度 × 负载余量
│ 5. 选择最优调度器      │
└──────────────────────┘
```

**关键技术点**:
- **Scheduler Resource Profile**: 定期（如 15s）汇总各调度器分区的总可分配/已使用资源
- **Pod 可行性过滤**: `Allocatable - Used ≥ Pod.Request` 在至少一个节点上成立
- **评分函数**: `Score(s, pod) = FeasibilityRatio(s, pod) × (1 - LoadRatio(s))`
- **缓存与增量更新**: 资源画像通过 Node Informer 事件增量维护，避免全量遍历

**学术价值**: 比 Kubernetes 原生调度（先分发再调度）增加了一层预筛选，将"必定失败的调度尝试"在 Dispatcher 层拦截，减少 E2E 延迟。

### 创新点 3：自适应并发控制的 Pod 分发（Adaptive Concurrency Control for Pod Dispatching）

**解决问题**: P6（goroutine 爆发）+ P3（锁争用）

**核心思想**: 用工作池 + 自适应令牌桶替代每 Pod 一个 goroutine，根据 API Server 延迟反馈动态调整并发度。

**设计**:

```
SortedPodsQueue
       │
       ▼
┌──────────────────────────────┐
│   Adaptive Worker Pool       │
│                              │
│  ┌────────────────────────┐  │
│  │ Worker 1 (goroutine)   │  │
│  │ Worker 2               │  │
│  │ ...                    │  │
│  │ Worker N (动态调整)    │  │
│  └────────────────────────┘  │
│                              │
│  Token Bucket:               │
│    rate = f(API latency P99) │
│    if P99 < 50ms → rate ↑   │
│    if P99 > 200ms → rate ↓  │
│                              │
│  Batch Accumulator:          │
│    同 Scheduler 的 Pod       │
│    合并为一次批量 Patch      │
└──────────────────────────────┘
```

**关键技术点**:
- **固定工作池**: 默认 `runtime.NumCPU()` 个 worker，避免 goroutine 爆发
- **AIMD 令牌桶**: API 延迟低时加法增大（Additive Increase），高时乘法减小（Multiplicative Decrease）
- **批量 Patch**: 同一 Scheduler 的 Pod 积累 batch（如 10ms 窗口或 50 个），减少 API 调用次数
- **调度器分组通道**: 每个 Scheduler 一个子 channel，避免 slow-scheduler 阻塞全局

**学术价值**: 将网络拥塞控制理论（AIMD）应用到 Kubernetes API 调用速率控制，在保护 API Server 的同时最大化分发吞吐量。

### 创新点 4：基于反馈的分区再平衡触发器（Feedback-Driven Partition Rebalancing）

**解决问题**: P4（固定周期）+ P1（粗糙均衡）的结合

**核心思想**: 用调度结果反馈（成功率、延迟、资源利用率）驱动分区再平衡，替代固定周期扫描。

**设计**:

```
信号采集:
  - scheduler_schedule_unit_result{result=failure}     → 调度失败率
  - scheduler_e2e_scheduling_duration_seconds           → 调度延迟
  - binder_embedded_bind_total{result=failure}          → 绑定失败率
  - scheduler_cluster_allocatable / cluster_requested   → 分区利用率
       │
       ▼
┌──────────────────────────────────┐
│  Imbalance Detector              │
│                                  │
│  定义不均衡度:                     │
│  I = Σ|metric_i - avg(metric)| / │
│       avg(metric)                │
│                                  │
│  if I > threshold:               │
│    → 触发 ReBalance              │
│    → 选择 metric 最差的分区      │
│    → 从 metric 最好的分区移入资源│
│                                  │
│  冷却期:                          │
│    上次再平衡后 cooldown 内不触发  │
└──────────────────────────────────┘
```

**学术价值**: 将控制理论中的闭环反馈应用到节点分区管理，实现"调度效果驱动的弹性分区"。

---

## 4. 实现规范

### 4.1 文件组织

```
pkg/dispatcher/
├── policy/
│   └── manager.go                      ← 增强：实现 DRF/FairShare 排序
├── node-shuffler/
│   ├── node_shuffler.go                ← 增强：资源感知再平衡
│   ├── resource_weight.go              ← 新增：节点资源权重计算
│   ├── imbalance_detector.go           ← 新增：不均衡度检测 + 反馈触发
│   └── partition_optimizer.go          ← 新增：分区优化目标求解（贪心/近似）
├── scheduler-maintainer/
│   ├── scheduler_maintainer.go         ← 增强：增加资源画像字段
│   ├── resource_profile.go             ← 新增：调度器分区资源画像
│   └── topology_scorer.go             ← 新增：拓扑感知评分
├── internal/
│   ├── store/
│   │   └── dispatch.go                 ← 增强：资源感知选调度器
│   └── worker/
│       ├── pool.go                     ← 新增：自适应工作池
│       └── rate_limiter.go             ← 新增：AIMD 令牌桶
└── metrics/
    └── metrics.go                      ← 增强：新增分区不均衡度/分发延迟指标
```

### 4.2 编码规范

- **保持向后兼容**: 所有新行为通过 Feature Gate 控制（`DispatcherResourceAwarePartition`, `DispatcherTopologyAwareSelection`, `DispatcherAdaptiveConcurrency`）
- **增量修改**: 不重写现有函数，通过新建函数 + 条件分支引入新逻辑
- **单一全局锁拆分**: 将 `schedulerMux` 拆为 `schedulerStateMux`（读多写少用 RWMutex）+ `nodePartitionMux`（写多用分片锁）
- **指标覆盖**: 每个创新点至少暴露 3 个 Prometheus 指标（用于论文定量评估）
- **测试覆盖**: 每个新增文件配对应 `_test.go`，覆盖正向/反向/边界用例

### 4.3 指标设计

```
# 创新点 1: 资源感知分区
dispatcher_partition_imbalance_ratio             gauge     分区不均衡度 (0=完美, 1=双倍偏差)
dispatcher_partition_rebalance_total             counter   再平衡触发次数
dispatcher_partition_node_weight                 gauge     per-scheduler 分区资源权重
dispatcher_partition_rebalance_duration_seconds  histogram 单次再平衡耗时

# 创新点 2: 拓扑感知选择
dispatcher_topology_match_score                  histogram Pod-Scheduler 匹配评分分布
dispatcher_topology_feasibility_filtered_total   counter   可行性过滤拦截的分发次数（避免的无效调度）
dispatcher_scheduler_resource_profile            gauge     per-scheduler 可用资源画像

# 创新点 3: 自适应并发控制
dispatcher_worker_pool_size                      gauge     当前工作池大小
dispatcher_token_bucket_rate                     gauge     当前令牌桶速率
dispatcher_batch_size                            histogram 批量 Patch 大小分布
dispatcher_api_latency_p99                       gauge     API Server 写延迟 P99 (令牌桶反馈信号)

# 创新点 4: 反馈驱动
dispatcher_feedback_signal_value                 gauge     各反馈信号当前值 (label: signal_type)
dispatcher_feedback_trigger_total                counter   反馈触发再平衡次数 (label: trigger_reason)
```

### 4.4 论文评估维度

| 维度 | 基线 (当前实现) | 改进后 | 测量方法 |
|------|----------------|--------|----------|
| 跨分区调度失败率 | 高（大Pod分配到小节点分区） | 降低 X% | `scheduler_schedule_unit_result{result=failure}` |
| E2E 分发延迟 P99 | 取决于 goroutine 爆发 | 降低 + 稳定 | `dispatcher_pod_dispatching_latency_seconds` |
| API Server QPS | 不可控尖峰 | 平滑可控 | `apiserver_request_total` |
| 分区资源偏差度 | 仅按节点数均衡 | 按加权资源均衡 | `dispatcher_partition_imbalance_ratio` |
| 再平衡响应时间 | 固定 1min | 数秒内（事件驱动） | `dispatcher_partition_rebalance_total` rate |
| 无效调度拦截 | 0% | X% 被预筛选拦截 | `dispatcher_topology_feasibility_filtered_total` |

---

## 5. 实现优先级

| 优先级 | 创新点 | 工作量 | 论文影响 |
|--------|--------|--------|----------|
| P0 | 创新点 1: 资源感知节点分区 | 中 | 高 — 直接改善调度成功率 |
| P0 | 创新点 2: 拓扑感知调度器选择 | 中 | 高 — 降低无效调度尝试 |
| P1 | 创新点 3: 自适应并发控制 | 中 | 中 — 保护 API Server 稳定性 |
| P1 | 创新点 4: 反馈驱动再平衡 | 小 | 高 — 闭环控制是理论亮点 |
| P2 | Policy Manager DRF 实现 | 大 | 中 — 公平调度是独立主题 |

---

## 6. 与已完成 Binder 改造的协同

嵌入式 Binder 改造（Phase 1-7）已完成，新增了 8 个 Prometheus 指标 + 27 条 Recording Rules。Dispatcher 增强改造可直接利用这些信号：

- `binder_embedded_bind_total{result=failure}` → 反馈驱动再平衡的绑定失败信号
- `scheduler_schedule_unit_result` → 拓扑感知选择的调度成功率反馈
- `scheduler_e2e_scheduling_duration_seconds` → 自适应并发控制的延迟反馈

这形成了完整的闭环优化链路：
```
Dispatcher (资源感知分发) → Scheduler (高效调度) → Embedded Binder (直接绑定)
     ↑                                                           │
     └───────── Prometheus 指标反馈 ◄────────────────────────────┘
```