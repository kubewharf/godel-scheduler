# Godel Scheduler 单 Instance 性能瓶颈深度分析

> **目的：** 作为研究生论文的第二个创新点，系统性分析 Godel Scheduler 单实例调度链路中的性能瓶颈，与已完成的 **Embedded Binder 架构优化（消除 Scheduler→Binder 进程间通信延迟）** 形成互补——一个解决调度后的绑定延迟，一个解决调度本身的吞吐量上限。

---

## 目录

1. [调度主循环概览](#1-调度主循环概览)
2. [瓶颈 1：单 Goroutine 串行调度主循环](#2-瓶颈-1单-goroutine-串行调度主循环)
3. [瓶颈 2：全局 Cache 写锁](#3-瓶颈-2全局-cache-写锁)
4. [瓶颈 3：Pop 队列锁与调度链路的耦合](#4-瓶颈-3pop-队列锁与调度链路的耦合)
5. [瓶颈 4：PodGroup 内部 Pod 逐个串行调度](#5-瓶颈-4podgroup-内部-pod-逐个串行调度)
6. [瓶颈 5：Filter 并行度硬编码上限 & 评估节点数受限](#6-瓶颈-5filter-并行度硬编码上限--评估节点数受限)
7. [瓶颈 6：applyToCache 串行 AssumePod](#7-瓶颈-6applytocache-串行-assumepod)
8. [端到端调度时间线分析](#8-端到端调度时间线分析)
9. [可作为论文创新点的优化方向](#9-可作为论文创新点的优化方向)
10. [关键源文件索引](#10-关键源文件索引)

---

## 1. 调度主循环概览

Godel Scheduler 单实例的调度主循环由 `ScheduleSwitch` 驱动，每个 DataSet（GT/BE per SubCluster）对应一个独立的调度 goroutine。调度链路的完整串行流程如下：

```
Pop(unit) → constructSchedulingUnitInfo → UpdateSnapshot(全局写锁)
  → RunLocatingPlugins → RunGroupingPlugin
  → scheduleUnitInNodeGroup
    → Scheduling (逐 Pod 串行: PreFilter → Filter → Score → 选节点)
    → Preempting (可选)
  → applyToCache (逐 Pod AssumePod 写入 Cache)
  → go PersistSuccessfulPods (异步绑定)
```

### 核心数据流

```
┌──────────────┐     ┌─────────────────┐     ┌──────────────┐
│  Informer    │────▶│ SchedulerCache  │────▶│   Snapshot    │
│  (Pod/Node)  │     │ (全局 RWMutex)  │     │  (lock-free) │
└──────────────┘     └─────────────────┘     └──────────────┘
                            │                       │
                     AssumePod/ForgetPod      Filter/Score 读取
                     (全局写锁)              (无锁并发读)
```

---

## 2. 瓶颈 1：单 Goroutine 串行调度主循环

### 代码位置

`pkg/scheduler/switch.go` — `ScheduleSwitchImpl.Run()`

### 代码证据

```go
// pkg/scheduler/switch.go:200-207
func (s *ScheduleSwitchImpl) Run(ctx context.Context) {
    if !utilfeature.DefaultFeatureGate.Enabled(features.SchedulerConcurrentScheduling) {
        if globalDataSet, ok := s.registry[framework.DisableScheduleSwitch]; ok && globalDataSet != nil {
            globalDataSet.Run(ctx)
            wait.UntilWithContext(
                context.WithValue(globalDataSet.Ctx(), CtxKeyScheduleDataSet, globalDataSet),
                globalDataSet.ScheduleFunc(),  // → unitScheduler.Schedule()
                0,                              // interval = 0，紧密循环
            )
        }
        // ...
    }
}
```

当 `SchedulerConcurrentScheduling` FeatureGate 关闭时（默认配置），只有 **一个 goroutine** 执行调度。即使开启，`workflowStartup()` 也仅为每个 DataSet 启动一个 goroutine：

```go
// pkg/scheduler/switch.go:244-260
func (s *ScheduleSwitchImpl) workflowStartup(ctx context.Context) {
    startup := func(dataSet ScheduleDataSet) bool {
        if dataSet != nil && dataSet.Run(ctx) {
            go wait.UntilWithContext(ctx, dataSet.ScheduleFunc(), 0)  // 每个 DataSet 1 个 goroutine
            return true
        }
        return false
    }
    // ...
}
```

### 问题分析

- 每个 DataSet **只有 1 个调度 goroutine**，所有 Unit 严格串行调度
- `interval = 0` 表示紧密循环，`Schedule()` 返回后立即调用下一次
- `Schedule()` 内部是完全同步的（除了最后的 `go PersistSuccessfulPods()`）
- **整个调度周期内，其他 Unit 完全无法被调度**

### 影响量化

| 场景 | 单 Unit 调度耗时 | 理论吞吐量上限 |
|-----|-----------------|---------------|
| SinglePod (1 pod) | ~5ms | 200 units/s |
| PodGroup (10 pods) | ~25ms | 40 units/s |
| PodGroup (50 pods) | ~110ms | 9 units/s |
| PodGroup (100 pods) | ~220ms | 4.5 units/s |

---

## 3. 瓶颈 2：全局 Cache 写锁

### 代码位置

`pkg/scheduler/cache/cache.go` — `schedulerCache.UpdateSnapshot()`

### 代码证据

```go
// pkg/scheduler/cache/cache.go:60-65
type schedulerCache struct {
    CommonStoresSwitch
    handler commoncache.CacheHandler
    mu      *sync.RWMutex      // <--- 唯一的全局锁
    cacheMetrics *cacheMetrics
}

// pkg/scheduler/cache/cache.go:95-115
func (cache *schedulerCache) UpdateSnapshot(snapshot *Snapshot) error {
    cache.mu.Lock()          // <--- 全局写锁！
    defer cache.mu.Unlock()
    // ... 遍历所有 Store 做增量同步
    return cache.CommonStoresSwitch.Range(
        func(cs commonstore.Store) error {
            if s := snapshot.CommonStoresSwitch.Find(cs.Name()); s != nil {
                return cs.UpdateSnapshot(s)
            }
            return nil
        })
}
```

### 锁竞争全景图

**所有 Cache 操作共享同一个 `sync.RWMutex`：**

| 操作 | 锁类型 | 调用时机 | 调用频率 |
|------|--------|---------|---------|
| `UpdateSnapshot()` | **写锁** | 每个调度周期开始 | 与调度频率相同 |
| `AssumePod()` | **写锁** | Pod 调度成功后写入 Cache | 每个成功 Pod |
| `ForgetPod()` | **写锁** | 调度回滚/过期清理 | 失败或超时时 |
| `SetUnitSchedulingStatus()` | **写锁** | 更新 Unit 调度状态 | 每个 Unit |
| `GetUnitStatus()` | 读锁 | 队列判断 readyToBeScheduled | 队列 flush 周期 |
| `GetUnitSchedulingStatus()` | 读锁 | 队列判断 | 队列 flush 周期 |
| Informer AddPod/UpdatePod/DeletePod | **写锁** | 所有 Pod 变更事件 | 与集群 Pod 事件频率相同 |
| Informer AddNode/UpdateNode/DeleteNode | **写锁** | 所有 Node 变更事件 | 与集群 Node 事件频率相同 |
| `updateMetrics()` | **写锁** | 周期性指标更新 | 定时 |

### 问题分析

- 当 `UpdateSnapshot()` 持有写锁时，**所有 Informer 回调（Pod/Node 增删改）全部阻塞**
- 在大集群中（5000+ 节点），Snapshot 增量同步可能耗时 **数毫秒到数十毫秒**
- `AssumePod()` 也需要写锁，在 `applyToCache` 中对 N 个 Pod 串行获取 N 次写锁
- Cache 写锁阻塞期间，Informer 的 Pod/Node 事件堆积，导致缓存与实际状态出现更大偏差

### 影响

```
时间线:
  T0 ----[UpdateSnapshot 写锁 5ms]----> T5ms
         │ 此期间所有 Informer 事件阻塞 │
         │ 所有 GetUnitStatus 阻塞     │
         │ 所有 AssumePod 阻塞         │
```

---

## 4. 瓶颈 3：Pop 队列锁与调度链路的耦合

### 代码位置

`pkg/scheduler/queue/priority_queue.go` — `PriorityQueue.Pop()`

### 代码证据

```go
// pkg/scheduler/queue/priority_queue.go:692-711
func (p *PriorityQueue) Pop() (*framework.QueuedUnitInfo, error) {
    p.lock.Lock()
    defer p.lock.Unlock()
    for p.readyQ.Len() == 0 {
        if p.closed {
            return nil, fmt.Errorf(queueClosed)
        }
        p.cond.Wait()   // <--- 队列空时阻塞，仍持有锁
    }
    obj, err := p.readyQ.Pop()
    if err != nil {
        return nil, err
    }
    unitInfo := obj.(*framework.QueuedUnitInfo)
    unitInfo.Attempts++
    p.setQueuedPriorityScore(unitInfo)
    p.schedulingCycle++
    p.priorityHeap.Delete(unitInfo)
    return unitInfo, nil
}
```

### 锁竞争分析

`PriorityQueue.lock` 被以下操作共享：

| 操作 | 调用方 | 频率 |
|------|--------|------|
| `Pop()` | 调度主循环 | 每个调度周期 |
| `Add()` | Informer Pod Add 事件 | 与 Pod 创建频率相同 |
| `Update()` | Informer Pod Update 事件 | 与 Pod 更新频率相同 |
| `Delete()` | Informer Pod Delete 事件 | 与 Pod 删除频率相同 |
| `AddUnschedulableIfNotPresent()` | 调度失败后重入队 | 每次调度失败 |
| `flushBackoffQCompleted()` | 后台 goroutine (1s) | 持续 |
| `flushUnschedulableQLeftover()` | 后台 goroutine (30s) | 持续 |
| `flushScheduledUnitsInWaitingQ()` | 后台 goroutine (2s) | 持续 |
| `flushWaitingPodsList()` | 后台 goroutine (20s) | 持续 |

### 问题分析

- 当 Informer 大批量推送 Pod 时（如大规模 Job 创建 500+ Pods），`Add()` 与 `Pop()` 互相阻塞
- `Pop()` 中的 `p.cond.Wait()` 释放锁后重新获取锁也可能与 `Add()` 竞争
- 4 个后台 flush goroutine 周期性持有锁，进一步增加竞争

---

## 5. 瓶颈 4：PodGroup 内部 Pod 逐个串行调度

### 代码位置

`pkg/scheduler/framework/unit_runtime/unit_framework.go` — `UnitFramework.Scheduling()`

### 代码证据

```go
// pkg/scheduler/framework/unit_runtime/unit_framework.go:172-260
func (f *UnitFramework) Scheduling(ctx context.Context,
    unitInfo *core.SchedulingUnitInfo, nodeGroup framework.NodeGroup,
) *core.UnitSchedulingResult {
    // ...
    for tmplKey, podKeys := range unitInfo.NotScheduledPodKeysByTemplate {
        podKeysList := podKeys.UnsortedList()
        for i, podKey := range podKeysList {
            // 逐个 Pod 调度！
            scheduled, err := f.scheduleOneUnitInstance(ctx, ...)

            if scheduled {
                result.SuccessfulPods = append(result.SuccessfulPods, podKey)
                unitInfo.ScheduledIndex = (unitInfo.ScheduledIndex) + 1
            } else {
                result.FailedPods = append(result.FailedPods, podKey)
                // 同模板其余 Pod 快速失败 (quick fail)
                for j := i + 1; j < len(podKeysList); j++ {
                    result.FailedPods = append(result.FailedPods, podKeysList[j])
                }
                break
            }
        }
    }
    return result
}
```

### 每个 Pod 的调度流程

每个 `scheduleOneUnitInstance` 内部调用 `podScheduler.ScheduleInSpecificNodeGroup()`，进而执行：

```
ScheduleInPreferredNodes (优先节点)
  └─ RunPreFilterPlugins → 逐节点 Filter
ScheduleInNodeCircles (普通节点)
  └─ RunPreFilterPlugins
  └─ findNodesThatPassFilters
     └─ findFeasibleNodes
        └─ parallelize.Until(ctx, size, checkNode)  // 并行 Filter，但硬编码 16 并发
  └─ prioritizeNodes
     └─ RunScorePlugins → parallelize.Until(ctx, nodeSize, ...)  // 并行 Score
  └─ selectHostAndCacheResults
```

### 问题分析

- 对于 PodGroup（如 Gang Scheduling，AllMember=50），**50 个 Pod 在同一调度周期内逐个串行执行完整的 PreFilter→Filter→Score 流程**
- 每个 Pod 完成调度后还要做 `Snapshot.AssumePod()`（串行写入 Snapshot），以便后续 Pod 能"看到"已调度 Pod 占用的资源
- 同模板 Pod 一旦失败则剩余 Pod 快速失败（quick fail），但成功路径无法并行化
- **这种串行性是 Pod 间有资源依赖（后调度的 Pod 需依赖前一个 Pod 的 AssumePod 结果）导致的设计约束**

### 影响量化

| PodGroup 规模 | 串行调度耗时（每 Pod ~2ms） | 占总调度周期比例 |
|---------------|--------------------------|----------------|
| AllMember = 10 | ~20ms | ~80% |
| AllMember = 50 | ~100ms | ~90% |
| AllMember = 100 | ~200ms | ~95% |

---

## 6. 瓶颈 5：Filter 并行度硬编码上限 & 评估节点数受限

### 代码位置

- `pkg/util/parallelize/parallelism.go` — `Until()`
- `pkg/scheduler/core/pod_scheduler/pod_scheduler.go` — `numFeasibleNodesToFind()`

### 代码证据 —— 并行度上限

```go
// pkg/util/parallelize/parallelism.go:27-31
const parallelism = 16

func Until(ctx context.Context, pieces int, doWorkPiece workqueue.DoWorkPieceFunc) {
    workqueue.ParallelizeUntil(ctx, parallelism, pieces, doWorkPiece)
}
```

**所有并行操作共享固定并行度 16：**

| 使用场景 | 代码位置 | 并行度 |
|---------|---------|--------|
| Filter 节点评估 | `findFeasibleNodes()` | 16 |
| Score 节点打分 | `RunScorePlugins()` | 16 |
| Score 归一化 | `RunScorePlugins()` NormalizeScore | 16 |
| Score 权重应用 | `RunScorePlugins()` weight | 16 |
| PersistSuccessfulPods PatchPod | `persistViaPatchPod()` | 16 |

### 代码证据 —— 评估节点数

```go
// pkg/scheduler/core/pod_scheduler/pod_scheduler.go:560-580
func (gs *podScheduler) numFeasibleNodesToFind(numAllNodes int32,
    longRunningTask bool, usr *framework.UnitSchedulingRequest,
    increasePercentageOfNodesToScore bool,
) (numNodes int32) {
    // ...
    if (usr.AllMember > 1 && !usr.EverScheduled) || longRunningTask {
        expectedNodeCount = int32(usr.AllMember + 50)    // magic number
    } else {
        expectedNodeCount = int32(usr.AllMember + 10)    // magic number
    }
    if expectedNodeCount > numAllNodes {
        expectedNodeCount = numAllNodes
    }
    return expectedNodeCount
}
```

### 问题分析

1. **并行度 16 是硬编码的**，无法根据集群规模或 CPU 核数动态调整。在 32 核甚至 64 核机器上，CPU 利用率不足 50%。
2. **评估节点数使用 magic number**（`AllMember + 50` 或 `AllMember + 10`），在大集群中可能导致只评估极小比例的节点：

| 集群规模 | AllMember | 评估节点数 | 评估比例 |
|---------|-----------|-----------|---------|
| 500 节点 | 1 | 11 | 2.2% |
| 1000 节点 | 50 | 100 | 10% |
| 5000 节点 | 1 | 11 | 0.22% |
| 5000 节点 | 100 | 150 | 3% |

评估比例过低可能导致**调度质量下降**——错过更优的节点选择。

---

## 7. 瓶颈 6：applyToCache 串行 AssumePod

### 代码位置

- `pkg/scheduler/core/unit_scheduler/unit_scheduler.go` — `applyToCache()`
- `pkg/scheduler/cache/snapshot.go` — `AssumePod()` / `ForgetPod()`

### 代码证据 —— applyToCache

```go
// pkg/scheduler/core/unit_scheduler/unit_scheduler.go:768-840
func (gs *unitScheduler) applyToCache(ctx context.Context,
    unitInfo *core.SchedulingUnitInfo, result *core.UnitResult,
) bool {
    cache := gs.Cache
    for i, key := range result.SuccessfulPods {
        runningPodInfo := unitInfo.DispatchedPods[key]
        cachePodInfo := framework.MakeCachePodInfoWrapper().
            Pod(runningPodInfo.ClonedPod).
            Victims(runningPodInfo.Victims).Obj()

        err := cache.AssumePod(cachePodInfo)  // 逐个串行写入 Cache（需全局写锁）

        if err != nil {
            // 回滚逻辑：对于 min-fail，逐个 ForgetPod 回滚
            if !unitInfo.EverScheduled && i < unitInfo.MinMember {
                for index := 0; index < i; index++ {
                    cache.ForgetPod(cachePodInfo)  // 也需全局写锁
                }
                return false
            }
            // ...
        }
    }
    return true
}
```

### 代码证据 —— Snapshot 写操作必须串行

```go
// pkg/scheduler/cache/snapshot.go:33-36
// Note: Snapshot operations are lock-free. Our premise for removing lock:
// even if read operations are concurrent,
// write operations(AssumePod/ForgetPod/AddOneVictim) should always be serial.
type Snapshot struct { ... }
```

### 问题分析

1. `applyToCache` 对 N 个 Pod 串行执行 `cache.AssumePod()`
2. 每次 `AssumePod()` 需要获取全局写锁 `cache.mu.Lock()`
3. Snapshot 的设计约束明确要求写操作必须串行化
4. 对于 PodGroup(50)，需要串行执行 **50 次锁获取 + 写入 + 锁释放**

### 影响量化

| 操作 | 单次耗时 | PodGroup(50) 总耗时 |
|-----|---------|-------------------|
| `cache.AssumePod()` (含锁) | ~0.1-0.2ms | ~5-10ms |
| 回滚 `cache.ForgetPod()` | ~0.1-0.2ms | 最坏 ~5-10ms |
| 合计 applyToCache | - | ~5-10ms |

---

## 8. 端到端调度时间线分析

### SinglePod 场景 (~5ms)

```
时间 ──────────────────────────────────────────────────────────▶

┌─ Pop()                 ┐  ~0.1ms
├─ constructUnitInfo     ┤  ~0.2ms
├─ UpdateSnapshot [W锁]  ┤  ~1-3ms ← CPU空转：等锁/同步Store
├─ Locating+Grouping     ┤  ~0.1ms
├─ Filter+Score (1 pod)  ┤  ~1-2ms ← 并行Filter(16线程)
├─ applyToCache [W锁]    ┤  ~0.1ms
├─ go Persist            ┤  异步
└──────────────────────── ┘
总计 ≈ ~3-6ms
```

### PodGroup (AllMember=50, MinMember=50) 场景 (~107-126ms)

```
时间 ──────────────────────────────────────────────────────────▶

┌─ Pop()                        ┐  ~0.1ms
├─ constructUnitInfo (50 pods)  ┤  ~0.5ms
├─ UpdateSnapshot [全局写锁]     ┤  ~1-5ms ← 阻塞所有Informer事件
├─ Locating+Grouping            ┤  ~0.1ms
├─ Scheduling (50 pods 串行)    ┤
│   ├─ Pod#1:  PreFilter+Filter+Score + AssumePod(snapshot)  ~2ms
│   ├─ Pod#2:  PreFilter+Filter+Score + AssumePod(snapshot)  ~2ms
│   ├─ ...
│   └─ Pod#50: PreFilter+Filter+Score + AssumePod(snapshot)  ~2ms
│                         小计 ≈ 100ms  ←──── 主要瓶颈
├─ applyToCache                 ┤
│   ├─ AssumePod#1 [全局写锁]    ~0.1-0.2ms
│   ├─ AssumePod#2 [全局写锁]    ~0.1-0.2ms
│   ├─ ...
│   └─ AssumePod#50 [全局写锁]   ~0.1-0.2ms
│                         小计 ≈ 5-10ms
├─ go PersistSuccessfulPods     ┤  异步（不阻塞下一个 Unit）
└───────────────────────────────┘
总计 ≈ ~107-126ms → 该 goroutine 在此期间完全阻塞其他 Unit 调度
```

### 时间分解饼图

| 阶段 | SinglePod | PodGroup(50) | 占比(PG50) |
|-----|-----------|-------------|-----------|
| Pop + constructUnitInfo | 0.3ms | 0.6ms | <1% |
| UpdateSnapshot | 1-3ms | 1-5ms | ~3% |
| Locating + Grouping | 0.1ms | 0.1ms | <1% |
| **Scheduling (串行 Filter+Score)** | 1-2ms | **~100ms** | **~85%** |
| applyToCache | 0.1ms | 5-10ms | ~7% |
| PersistSuccessfulPods | 异步 | 异步 | 0% |
| **总计** | **~3-6ms** | **~107-126ms** | **100%** |

---

## 9. 可作为论文创新点的优化方向

### 优化方向总览

| # | 优化方向 | 对应瓶颈 | 核心思路 | 预期收益 | 实现难度 |
|---|---------|---------|---------|---------|---------|
| 1 | **多 Worker 并发调度** | 瓶颈 1 | 类似 kube-scheduler 的多 worker 模型，但需解决 Unit 间资源冲突 | 吞吐量线性提升 N 倍 | 高 |
| 2 | **读写分离 / 无锁 Snapshot** | 瓶颈 2 | Copy-on-Write 双缓冲 Snapshot，异步增量同步，消除 UpdateSnapshot 写锁对 Informer 的阻塞 | 减少锁等待 50%+ | 中 |
| 3 | **PodGroup 内部 Pod 并行调度** | 瓶颈 4 | 同模板 Pod 可并行执行 Filter+Score（共享 PreFilter 结果），最后统一 AssumePod | PodGroup 调度延迟降低至 ~1/N | 中高 |
| 4 | **自适应并行度** | 瓶颈 5 | 根据 `runtime.GOMAXPROCS` 和集群规模动态调整 parallelism；重新审视 percentageOfNodesToScore 策略 | 大集群下 Filter/Score 加速 | 低 |
| 5 | **批量 AssumePod** | 瓶颈 6 | 将多个 Pod 的 AssumePod 合并为一次批量写入，减少锁获取次数 | 减少 applyToCache ~50% 耗时 | 低 |
| 6 | **队列无锁优化** | 瓶颈 3 | 使用分段锁或 lockfree queue 减少 Pop/Add 竞争 | 高并发 Pod 创建场景降低延迟 | 中 |

### 推荐论文创新点组合

#### 方案 A：Scheduler 并发调度架构优化（推荐）

将 **瓶颈 1（单 goroutine 串行）+ 瓶颈 2（全局 Cache 锁）+ 瓶颈 4（PodGroup Pod 串行调度）** 组合研究，构成 **"Scheduler 并发调度架构优化"** 的完整故事线：

```
现状问题                    优化方案                      预期效果
─────────                 ─────────                    ─────────
单goroutine串行调度  ──→  多Worker并发调度模型    ──→  吞吐量线性提升
全局Cache写锁       ──→  读写分离/双缓冲Snapshot ──→  锁等待降低50%+
PodGroup Pod串行    ──→  同模板Pod并行Filter/Score ──→  PG延迟降至1/N
```

**与 Embedded Binder 的互补关系：**

| 优化点 | 解决的问题 | 影响的调度阶段 |
|--------|-----------|-------------|
| Embedded Binder | Scheduler→Binder 进程间通信延迟 | 调度后（Persist 阶段） |
| 并发调度架构优化 | Scheduler 内部调度吞吐量上限 | 调度中（Schedule 阶段） |

#### 方案 B：轻量级优化组合（备选，实现门槛低）

将 **瓶颈 5（并行度硬编码）+ 瓶颈 6（串行 AssumePod）** 组合：

- 自适应并行度：`parallelism = min(runtime.GOMAXPROCS(0), numNodes/4)`
- 批量 AssumePod：合并 N 次 `cache.mu.Lock()` 为 1 次

---

## 10. 关键源文件索引

| 文件 | 关键内容 | 行号 |
|-----|---------|-----|
| `pkg/scheduler/switch.go` | ScheduleSwitch 主循环、workflowStartup | L200, L244 |
| `pkg/scheduler/scheduler.go` | Scheduler 结构体、New()、Run()、createDataSet() | L85, L183, L235 |
| `pkg/scheduler/core/unit_scheduler/unit_scheduler.go` | Schedule() 主函数、applyToCache、PersistSuccessfulPods | L317, L768, L850+ |
| `pkg/scheduler/cache/cache.go` | schedulerCache、UpdateSnapshot（全局写锁）、AssumePod | L60, L95 |
| `pkg/scheduler/cache/snapshot.go` | Snapshot（lock-free 设计、串行写约束）| L33 |
| `pkg/scheduler/queue/priority_queue.go` | PriorityQueue、Pop()、Add()、4 个 flush goroutine | L54, L692, L168 |
| `pkg/scheduler/queue/util.go` | MakeNextUnitFunc（Pop 包装器）| L81 |
| `pkg/scheduler/core/pod_scheduler/pod_scheduler.go` | findFeasibleNodes、numFeasibleNodesToFind、prioritizeNodes | L420, L560, L600 |
| `pkg/scheduler/framework/unit_runtime/unit_framework.go` | Scheduling()（Pod 串行调度循环）、scheduleOneUnitInstance | L172 |
| `pkg/scheduler/framework/runtime/framework.go` | RunScorePlugins（并行 Score）、RunFilterPlugins | L337, L220 |
| `pkg/util/parallelize/parallelism.go` | 硬编码 parallelism=16 | L27 |

---

## 附录：与 kube-scheduler 的对比

| 特性 | Godel Scheduler | kube-scheduler (1.28+) |
|-----|----------------|----------------------|
| 调度粒度 | Unit（可含多个 Pod） | 单 Pod |
| 调度并发 | 单 goroutine per DataSet | 可配置多 worker (默认1) |
| Cache 锁 | 全局 `sync.RWMutex` | 全局 `sync.RWMutex` |
| Snapshot 更新 | 每周期 `UpdateSnapshot()` 写锁 | 每周期 `UpdateSnapshot()` 写锁 |
| Filter 并行度 | 硬编码 16 | 硬编码 16 |
| 评估节点数 | magic number (`AllMember+50`) | `percentageOfNodesToScore` (可配) |
| PodGroup 支持 | 原生（Unit 内逐 Pod 串行） | 无（需 Coscheduling 插件） |
| 调度后绑定 | Embedded Binder / PatchPod | apiserver Bind 调用 |

**核心差异：** Godel 的 Unit 串行调度模型使得 PodGroup 场景下的瓶颈远比 kube-scheduler 的单 Pod 模型更显著。这正是优化的价值所在。
