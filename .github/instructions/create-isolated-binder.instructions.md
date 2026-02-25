---
description: 当涉及 Binder 架构改造、Scheduler-Binder 集成、绑定性能优化、或新增/修改 pkg/binder、pkg/scheduler 中与 Pod 绑定相关的代码时，加载此指令
applyTo: '**/{binder,scheduler}/**/*.go'
---

# Gödel Scheduler 独立 Binder 架构改造指令

## 1. 改造背景与动机

### 1.1 当前架构（共享 Binder）的瓶颈

Gödel Scheduler 采用三级流水线架构：**Dispatcher → Scheduler → Binder**。

```
Dispatcher (1 个)
    ├── Scheduler A (Node Partition 1)  ──┐
    ├── Scheduler B (Node Partition 2)  ──┼──→ Shared Binder (1 个) → API Server
    └── Scheduler C (Node Partition 3)  ──┘
```

**核心问题：**
- Scheduler 完成调度后，通过 `PatchPod`（写 `godel.bytedance.com/assumed-node` annotation）将决策持久化到 API Server
- Binder 通过 Informer 监听 `AssumedPodOfGodel` 状态的 Pod，拉入 BinderQueue 进行冲突检测和绑定
- **所有 Scheduler 的绑定请求汇聚到同一个 Binder 实例**，形成单点瓶颈
- Binder 使用独立的 BinderCache，与 Scheduler 的 SchedulerCache 完全隔离，存在状态同步延迟

### 1.2 改造目标

**每个 Scheduler 实例拥有独立的 Binder**，Binder 内嵌到 Scheduler 进程中，共享 Cache，实现：

```
Dispatcher (1 个)
    ├── Scheduler A + Binder A (Node Partition 1) → API Server
    ├── Scheduler B + Binder B (Node Partition 2) → API Server
    └── Scheduler C + Binder C (Node Partition 3) → API Server
```

### 1.3 学术价值（论文亮点）

此改造是研究生论文的核心贡献之一，需要从以下角度论证：

1. **消除集中式瓶颈**：共享 Binder 的队列竞争、串行处理是可量化的性能瓶颈
2. **线性可扩展性**：绑定吞吐量随 Scheduler 数量线性增长
3. **状态一致性提升**：共享 Cache 消除了 Scheduler ↔ Binder 之间的状态同步延迟
4. **故障域隔离**：单个 Binder 故障不再影响全局调度

---

## 2. 当前架构关键代码路径

### 2.1 Scheduler → API Server（调度决策持久化）

**关键文件**：`pkg/scheduler/core/unit_scheduler/unit_scheduler.go`

调度完成后，Scheduler 的动作：

```
ReservePod() → 设置 Pod Annotations:
  - godel.bytedance.com/pod-state = "assumed"
  - godel.bytedance.com/assumed-node = "<nodeName>"
  - 写入 Snapshot（AssumePod）

PatchPod() → 将 annotations Patch 到 API Server
  - 这是 Scheduler → Binder 的唯一通信通道
  - Binder 通过 Informer 监听此变更
```

**核心代码**（`unit_scheduler.go` ~L220-230）：
```go
clonedPod.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodAssumed)
clonedPod.Annotations[podutil.AssumedNodeAnnotationKey] = scheduleResult.SuggestedHost
```

**核心代码**（`unit_scheduler.go` ~L806）：
```go
err := util.PatchPod(gs.client, runningUnitInfo.QueuedPodInfo.Pod, runningUnitInfo.ClonedPod)
```

### 2.2 Binder 监听与绑定

**关键文件**：`pkg/binder/godel_binder.go`, `pkg/binder/eventhandlers.go`

Binder 的工作流程：

```
1. Informer 监听 Pod 变更
2. eventhandlers.go 过滤 AssumedPodOfGodel 状态的 Pod
3. 加入 BinderQueue
4. CheckAndBindUnit() 取出 Unit 处理：
   a. 冲突检测（CheckTopology, CheckConflicts）
   b. Volume 绑定（AssumePodVolumes）
   c. 设置 pod.Spec.NodeName = suggestedNode
   d. AssumePod 到 BinderCache
   e. 异步执行 Bind API 调用
```

**核心代码**（`godel_binder.go` ~L812）：
```go
cr.runningUnit.clonedPod.Spec.NodeName = cr.runningUnit.suggestedNode
```

### 2.3 PodGroupController（绑定侧）

**关键文件**：`pkg/binder/controller/podgroup.go`

- 由 Binder 进程启动（`cmd/binder/app/server.go:225`）
- 管理 PodGroup 状态转换（Pending → Scheduled → Running / Timeout）
- 设置 `EverScheduled` 标志，影响后续调度轮次

### 2.4 部署清单

| 组件 | 当前部署 | 文件 |
|------|---------|------|
| Scheduler | Deployment, replicas=1 | `manifests/base/deployment/scheduler.yaml` |
| Binder | Deployment, replicas=1, **leader-elect=false** | `manifests/base/deployment/binder.yaml` |
| Dispatcher | Deployment, replicas=1 | `manifests/base/deployment/dispatcher.yaml` |

**注意**：Binder 当前是单副本部署（`replicas: 1`），不支持多副本的核心原因是 BinderCache 中的 Assumed Pods 状态是内存态，多副本会导致状态不一致。

### 2.5 两套独立 Cache

| Cache | 所在组件 | 用途 |
|-------|---------|------|
| SchedulerCache | Scheduler | 存储 Assumed Pods（调度决策），用于避免 double-booking |
| BinderCache | Binder | 存储 Assumed Pods（绑定阶段），用于冲突检测 |

**改造后共享 Cache** 是最大的架构简化：Scheduler 的 AssumePod 结果不再需要通过 API Server → Informer 链路传播到 Binder。

---

## 3. 改造设计原则

### 3.1 核心约束

1. **节点分区隔离是前提**：Gödel 的 NodeShuffler 已经将节点通过 `godel.bytedance.com/scheduler-name` annotation 分配给不同 Scheduler，保证了不同 Scheduler 不会调度到同一节点，因此独立 Binder 不会产生跨 Scheduler 的绑定冲突
2. **向后兼容**：通过配置开关支持新旧架构切换，不改变任何外部 CRD/API
3. **PodGroupController 需要迁移**：当前在 Binder 中运行的 PodGroupController 需要跟随 Binder 一起内嵌到 Scheduler 中，或改为独立控制器
4. **API Server 乐观锁仍是最终一致性保障**：即便在节点重分区（reshuffle）期间出现短暂的边界情况，API Server 的 ResourceVersion 冲突检测仍然是兜底机制

### 3.2 需要改造的组件

| 组件 | 改造内容 | 优先级 |
|------|---------|--------|
| `cmd/scheduler/app/server.go` | 启动时创建并启动内嵌 Binder | P0 |
| `pkg/scheduler/scheduler.go` | Scheduler 结构体新增 Binder 字段 | P0 |
| `pkg/binder/godel_binder.go` | 支持内嵌模式，共享 Cache 接口 | P0 |
| `pkg/scheduler/core/unit_scheduler/unit_scheduler.go` | 调度成功后直接调用 Binder（不再 PatchPod） | P0 |
| `pkg/binder/eventhandlers.go` | 内嵌模式下不再通过 Informer 监听 | P1 |
| `pkg/binder/controller/podgroup.go` | PodGroupController 迁移策略 | P1 |
| `manifests/base/deployment/` | 更新部署清单 | P1 |
| `cmd/scheduler/app/options/` | 新增 Binder 相关 CLI Flags | P2 |

### 3.3 不需要改造的组件

| 组件 | 原因 |
|------|------|
| Dispatcher | 仍然独立运行，不受影响 |
| NodeShuffler | 节点分区机制不变 |
| CRD API | PodGroup、Scheduler 等 CRD 不变 |

---

## 4. Trade-off 分析

### 4.1 收益

| 维度 | 共享 Binder | 独立 Binder | 量化改进 |
|------|-----------|-----------|---------|
| 绑定吞吐量 | 单点限制：~1000 pods/s | N × 单点：~N×1000 pods/s | **N 倍** |
| 绑定延迟 | 队列等待 + 处理 | 仅处理（无队列竞争） | **降低 50%+** |
| Assumed 状态同步 | API Server → Informer 链路（秒级） | 共享内存（纳秒级） | **3个数量级** |
| 故障爆炸半径 | Binder 故障 → 全部停止 | 单个故障 → 单个分区 | **1/N** |
| 部署复杂度 | 2个 Deployment | 1个 Deployment | **简化** |

### 4.2 代价

| 维度 | 影响 | 缓解措施 |
|------|------|---------|
| 内存占用 | 每个 Scheduler 进程额外内存（BinderCache） | 内嵌模式下共享 Cache，实际增量很小 |
| PodGroupController 一致性 | 多个 PodGroupController 实例可能竞争更新同一 PodGroup | 使用 API Server 乐观锁 + 仅更新自己分区的 Pod 状态 |
| 节点重分区时的边界条件 | reshuffle 瞬间可能存在短暂的双写 | 绑定前校验节点分区归属 |
| 代码复杂度 | Scheduler 和 Binder 耦合度增加 | 清晰的接口隔离，Binder 作为 Scheduler 的子模块 |

### 4.3 关键 Trade-off 决策

**决策 1：PodGroupController 归属**
- 方案 A：内嵌到 Scheduler（每个实例各一份）→ 简单但有多写竞争
- 方案 B：保持独立控制器（全局唯一）→ 复杂但无竞争
- **推荐 A**：利用 API Server ResourceVersion 解决竞争，且各 Scheduler 只关心自己分区的 Pod 即可

**决策 2：通信方式**
- 方案 A：Scheduler 直接函数调用 Binder（同进程）→ 延迟最低
- 方案 B：Scheduler 仍通过 API Server Annotation，Binder 仍通过 Informer → 兼容性好但无性能提升
- **推荐 A**：这是架构改造的核心目标

**决策 3：Cache 共享策略**
- 方案 A：统一为一套 Cache → 大量重构
- 方案 B：BinderCache 包装 SchedulerCache → 增量改动
- **推荐 B**：通过适配器模式让 BinderCache 底层复用 SchedulerCache

---

## 5. 分阶段实施计划

### Phase 1：接口定义与配置开关（Week 1-2）

**目标**：定义 Binder 接口抽象层，支持切换内嵌/独立模式

- [ ] 在 `pkg/binder/` 下定义 `BinderInterface`
- [ ] 在 Scheduler Options 中新增 `--enable-embedded-binder` 开关
- [ ] 定义 `BindRequest` 结构体（Pod、NodeName、Victims 等）
- [ ] 为 SchedulerCache 和 BinderCache 抽取公共接口

### Phase 2：核心集成（Week 3-4）

**目标**：Scheduler 内嵌 Binder，共享 Cache

- [ ] `pkg/scheduler/scheduler.go`：Scheduler 结构体增加 `embeddedBinder` 字段
- [ ] `cmd/scheduler/app/server.go`：启动时根据配置创建内嵌 Binder
- [ ] 实现 Cache 适配器：BinderCache → SchedulerCache 包装
- [ ] `unit_scheduler.go`：调度成功后直接调用 `binder.BindUnit()` 而非 `PatchPod`
- [ ] 内嵌模式下绕过 Informer 路径，直接将 BindRequest 推入 Binder 队列

### Phase 3：PodGroupController 迁移（Week 5-6）

**目标**：PodGroupController 跟随 Binder 内嵌

- [ ] 将 PodGroupController 初始化从 `cmd/binder/app/server.go` 抽取为可复用逻辑
- [ ] 在 Scheduler 启动时初始化 PodGroupController
- [ ] 添加分区过滤：只处理本分区 Pod 的 PodGroup 状态变更
- [ ] 处理多实例竞争更新的边界情况

### Phase 4：绑定前节点验证（Week 7）

**目标**：防止节点重分区时的边界问题

- [ ] 实现 `NodeValidator`：绑定前检查节点 `godel.bytedance.com/scheduler-name` annotation
- [ ] 若节点不再属于当前 Scheduler，拒绝绑定并重新入队
- [ ] 添加相关 metrics（`godel_binder_node_validation_failures_total`）

### Phase 5：部署与测试（Week 8-10）

**目标**：验证正确性和性能

- [ ] 更新 `manifests/`：合并 Scheduler + Binder 部署
- [ ] 编写 integration test：验证多 Scheduler 并行绑定
- [ ] 性能基准测试：对比共享 vs 独立 Binder 的吞吐量和延迟
- [ ] 灰度验证：配置开关支持快速回滚

---

## 6. 编码规范

### 6.1 新增代码位置

```
pkg/binder/
├── embedded_binder.go       # 内嵌 Binder 实现
├── binder_interface.go      # Binder 接口定义
├── node_validator.go        # 绑定前节点验证
├── cache_adapter.go         # SchedulerCache → BinderCache 适配器
└── ...（现有文件保持不动）

pkg/scheduler/
├── scheduler.go             # 新增 embeddedBinder 字段
└── core/unit_scheduler/
    └── unit_scheduler.go    # 修改绑定路径

cmd/scheduler/app/
├── server.go                # 新增 Binder 启动逻辑
└── options/
    └── options.go           # 新增 Binder 配置选项
```

### 6.2 日志规范

- 所有 Binder 相关日志必须包含 `"scheduler"` 字段标识所属 Scheduler
- 关键路径使用 `klog.V(2)`，调试路径使用 `klog.V(4)`
- 绑定成功/失败使用 `klog.V(3).InfoS`

### 6.3 Metrics 规范

所有新增 metrics 必须包含 `scheduler` label，以区分不同 Scheduler 实例：

```go
BindingLatency    = prometheus.NewHistogramVec(..., []string{"scheduler", "result"})
BindingQueueLen   = prometheus.NewGaugeVec(..., []string{"scheduler"})
BindingTotal      = prometheus.NewCounterVec(..., []string{"scheduler", "result"})
NodeValidationErr = prometheus.NewCounterVec(..., []string{"scheduler", "reason"})
```

### 6.4 测试要求

- 每个新增/修改的核心函数必须有单元测试
- 内嵌 Binder 路径需要与独立 Binder 路径功能等价性测试
- 性能测试结果作为论文数据，需要可复现

---

## 7. 关键注意事项

### 7.1 绝对不能改动的

- Gödel CRD API（PodGroup, Scheduler, Movement 等）
- Dispatcher 的调度单元分发逻辑
- NodeShuffler 的节点分区逻辑
- Pod 的 annotation 键名（`godel.bytedance.com/*`）

### 7.2 必须保持的行为等价性

- 绑定结果必须与共享 Binder 完全一致
- PodGroup 状态机转换不变
- 冲突检测逻辑不变（CheckTopology, CheckConflicts）
- Volume 绑定逻辑不变

### 7.3 已知的坑

1. **BinderCache 的 PodAssumedTTL**（当前 5 分钟）：内嵌模式下共享 Cache 后，TTL 语义需要重新评估
2. **Reconciler**（`BinderTasksReconciler`）：负责清理过期的绑定任务，内嵌后需要确保仍然运行
3. **Binder 的 leader-elect 当前是 false**：内嵌后不再需要独立的 leader 选举
4. **`SchedulerName` 字段**：Binder 使用 `SchedulerName` 过滤 Pod（`AssumedPodOfGodel`），内嵌后这个过滤逻辑需要与 Scheduler 保持一致

---

## 8. 容错机制：失败回退 Dispatcher 重分配

### 8.1 当前容错链路分析

当前 Gödel 已有两层容错机制，但在独立 Binder 架构下需要增强：

#### 8.1.1 Scheduler 调度失败 → 回退 Dispatcher

**关键代码**：`pkg/scheduler/core/unit_scheduler/internal.go` `updateFailedSchedulingPod()`

当 `DispatchToAnotherScheduler = true` 时（当前仅 disablePreemption 时生效）：

```go
// internal.go ~L70-78
failedSchedulerSet := podutil.GetFailedSchedulersNames(failedPod)
failedSchedulerSet.Insert(schedulerName)  // 记录失败的 Scheduler
podCopy.Annotations[podutil.FailedSchedulersAnnotationKey] = strings.Join(failedSchedulerSet.List(), ",")
podCopy.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodPending)  // 重置为 Pending
delete(podCopy.Annotations, podutil.SchedulerAnnotationKey)  // 清除 Scheduler 分配标记
```

这会将 Pod 状态回退到 Pending，Dispatcher 的 Informer 会重新捕获并分配给另一个 Scheduler。

当 `DispatchToAnotherScheduler = false`（默认）时：
- Pod 重新入队本 Scheduler 的内部队列（`AddUnschedulableIfNotPresent`）
- 不会回退 Dispatcher

#### 8.1.2 Binder 绑定失败 → 回退 Scheduler

**关键代码**：`pkg/binder/utils/util.go` `CleanupPodAnnotations()`

```go
// 清除调度决策 annotations
delete(podCopy.Annotations, podutil.AssumedNodeAnnotationKey)
delete(podCopy.Annotations, podutil.NominatedNodeAnnotationKey)
delete(podCopy.Annotations, podutil.FailedSchedulersAnnotationKey)
// 重置状态为 Dispatched（不是 Pending！）
podCopy.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodDispatched)
```

**关键问题**：Binder 失败后状态重置为 `Dispatched`，Pod 会回到**同一个 Scheduler** 重新调度，而不是回退到 Dispatcher。这意味着如果问题是 Scheduler 所在节点分区资源不足，Pod 会在同一个 Scheduler 中无限重试。

### 8.2 当前机制的不足

```
失败场景                         当前行为                         期望行为
─────────────────────────────────────────────────────────────────────────────────
Scheduler 调度失败（有抢占）     本地重试，不回退 Dispatcher      N 次失败后回退 Dispatcher
Scheduler 调度失败（无抢占）     回退 Dispatcher                   ✓ 已有此机制
Binder 冲突检测失败              回退同一 Scheduler               N 次失败后回退 Dispatcher
Binder Volume 绑定失败           回退同一 Scheduler               N 次失败后回退 Dispatcher  
Binder 进程故障（独立 Binder）   全局停止                          独立 Binder 仅影响单分区
Scheduler+Binder 进程故障        该分区停止                        Dispatcher 应感知并重分配
```

### 8.3 独立 Binder 架构必须增强的容错

#### 8.3.1 多次调度/绑定失败 → 回退 Dispatcher

**设计**：引入 `MaxLocalRetries` 参数，当 Pod 在同一 Scheduler+Binder 中累计失败次数超过阈值时，回退 Dispatcher 重新分配。

**实现要点**：

```go
// unit_scheduler.go 调度失败处理路径
// 增强现有逻辑：不再仅 disablePreemption 时才回退
if unitInfo.QueuedUnitInfo.Attempts >= MaxLocalRetries {
    unitInfo.DispatchToAnotherScheduler = true  // 交回 Dispatcher
    klog.V(2).InfoS("Max local retries exceeded, dispatching to another scheduler",
        "unitKey", unitInfo.UnitKey, "attempts", unitInfo.QueuedUnitInfo.Attempts)
}
```

```go
// binder CleanupPodAnnotations 增强
// Binder 连续失败时，不再回退到同一 Scheduler，而是回退到 Dispatcher
if binderRetryCount >= MaxBinderRetries {
    podCopy.Annotations[podutil.PodStateAnnotationKey] = string(podutil.PodPending)  // Pending 而非 Dispatched
    delete(podCopy.Annotations, podutil.SchedulerAnnotationKey)  // 清除 Scheduler 标记
}
```

#### 8.3.2 Scheduler+Binder 进程健康检测

**设计**：Dispatcher 的 SchedulerMaintainer 已有 Scheduler 心跳检测机制。独立 Binder 架构下，Scheduler 进程故障等同于 Binder 故障，Dispatcher 需要：

1. 检测到 Scheduler 不可用后，将其节点分区重新分配
2. 将该 Scheduler 上所有 Dispatched/Assumed 状态的 Pod 回收，重置为 Pending
3. 通过 `FailedSchedulersAnnotationKey` 避免再次分配到同一（恢复后的）Scheduler

#### 8.3.3 Binder 内部重试与熔断

**设计**：内嵌 Binder 应有独立的重试和熔断机制：

```go
type EmbeddedBinderConfig struct {
    // 单个 Pod 在 Binder 内的最大重试次数
    MaxBindRetries int  // 默认 3（当前硬编码为 MaxRetryAttempts=3）
    
    // 绑定超时时间
    BindTimeout time.Duration  // 默认 30s
    
    // 连续失败 N 次后，触发回退 Dispatcher 的阈值
    MaxLocalRetries int  // 默认 5（Scheduler + Binder 累计）
    
    // Binder 队列积压报警阈值
    QueueBacklogThreshold int  // 默认 100
}
```

#### 8.3.4 失败信息传播

**设计**：失败原因需要在 Scheduler → Binder → Dispatcher 链路中完整传播：

```go
// Pod Annotations 中记录失败历史
const (
    // 已有
    FailedSchedulersAnnotationKey = "godel.bytedance.com/failed-schedulers"  // 记录哪些 Scheduler 失败过
    
    // 新增
    BindFailureCountAnnotationKey = "godel.bytedance.com/bind-failure-count"  // Binder 累计失败次数
    LastBindFailureReasonKey      = "godel.bytedance.com/last-bind-failure"   // 最近一次绑定失败原因
)
```

Dispatcher 重新分配时可以参考这些信息，避免分配到同类型的 Scheduler 分区（例如资源不足的分区）。

### 8.4 容错状态机

```
                    ┌──────────────────────────────────┐
                    │           Dispatcher              │
                    │  (分配 Pod 到 Scheduler)          │
                    └──────────┬───────────────────────┘
                               │ dispatch
                               ▼
                    ┌──────────────────────────────────┐
                    │    Scheduler + Embedded Binder     │
                    │                                    │
                    │  ┌─ Schedule ──→ Bind ──→ 成功 ─┐ │
                    │  │       ↓           ↓           │ │
                    │  │   失败(重试<N)  失败(重试<M)   │ │
                    │  │       ↓           ↓           │ │
                    │  └── 本地重试 ←────────┘         │ │
                    │                                    │
                    │  ┌─ 累计失败 ≥ MaxLocalRetries ───┐│
                    │  │  设置 DispatchToAnother=true    ││
                    │  │  记录 FailedSchedulers          ││
                    │  │  状态回退 PodPending             ││
                    │  └────────────────────────────────┘│
                    └──────────┬───────────────────────┘
                               │ 回退
                               ▼
                    ┌──────────────────────────────────┐
                    │           Dispatcher              │
                    │  排除已失败的 Scheduler            │
                    │  重新分配到健康的 Scheduler         │
                    └──────────────────────────────────┘
```

### 8.5 实施优先级

| 任务 | 优先级 | 阶段 |
|------|--------|------|
| Scheduler 多次失败回退 Dispatcher（`MaxLocalRetries`） | **P0** | Phase 2 |
| Binder 失败回退 Dispatcher（而非同一 Scheduler） | **P0** | Phase 2 |
| 失败信息 Annotation 传播 | **P1** | Phase 2 |
| Dispatcher 感知 Scheduler 故障并回收 Pod | **P1** | Phase 3 |
| Binder 熔断机制 | **P2** | Phase 4 |
| 失败 metrics 与告警 | **P2** | Phase 5 |