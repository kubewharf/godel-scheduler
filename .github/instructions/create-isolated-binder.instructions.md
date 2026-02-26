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
- [ ] 定义 `BindRequest` / `BindResult` 结构体（Pod、NodeName、Victims 等）
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

### Phase 4：容错与节点验证（Week 7-8）

**目标**：防止节点重分区时的边界问题，并增强失败回退能力

- [ ] 实现 `NodeValidator`：绑定前检查节点 `godel.bytedance.com/scheduler-name` annotation
- [ ] 若节点不再属于当前 Scheduler，拒绝绑定并重新入队
- [ ] 实现 `MaxLocalRetries` 回退 Dispatcher 机制
- [ ] 增强 `CleanupPodAnnotations`：超过重试阈值时回退 Dispatcher 而非同一 Scheduler
- [ ] 添加相关 metrics（`godel_binder_node_validation_failures_total`）

### Phase 5：部署与测试（Week 9-12）

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
├── embedded_binder.go           # 内嵌 Binder 实现
├── embedded_binder_test.go      # 内嵌 Binder 单元测试
├── binder_interface.go          # Binder 接口定义
├── binder_interface_test.go     # 接口契约测试
├── node_validator.go            # 绑定前节点验证
├── node_validator_test.go       # 节点验证单元测试
├── cache_adapter.go             # SchedulerCache → BinderCache 适配器
├── cache_adapter_test.go        # Cache 适配器单元测试
└── ...（现有文件保持不动）

pkg/binder/utils/
├── util.go                      # 增强 CleanupPodAnnotations
├── util_test.go                 # 新增回退 Dispatcher 测试（当前缺少此文件）
├── retry.go                     # 重试与失败计数逻辑
└── retry_test.go                # 重试逻辑单元测试

pkg/scheduler/
├── scheduler.go                 # 新增 embeddedBinder 字段
├── scheduler_test.go            # 新增内嵌 Binder 启停测试
└── core/unit_scheduler/
    ├── unit_scheduler.go        # 修改绑定路径
    └── unit_scheduler_test.go   # 新增内嵌绑定路径测试

cmd/scheduler/app/
├── server.go                    # 新增 Binder 启动逻辑
└── options/
    └── options.go               # 新增 Binder 配置选项
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

---

## 7. 单元测试规范（全局要求）

### 7.1 测试基础设施（必须遵循）

本项目已有成熟的测试基础设施，**所有新增测试必须复用**，禁止重复造轮子：

| 基础设施 | 位置 | 用途 |
|----------|------|------|
| `testing-helper/wrappers.go` | `pkg/testing-helper/` | Builder 模式构造测试数据：`MakePod().Name("x").Namespace("y").Node("z").Obj()` |
| `testing-helper/fake.go` | `pkg/testing-helper/` | Fake Listers：`NewFakePodGroupLister` 等 |
| `binder/cache/fake/fake_cache.go` | `pkg/binder/cache/fake/` | FakeBinderCache：函数字段注入模式 |
| `binder/testing/framework_helpers.go` | `pkg/binder/testing/` | `MockBinderFrameworkHandle`、`NewBinderFramework()` |
| `scheduler/testing/fake_plugins.go` | `pkg/scheduler/testing/` | `FalseFilterPlugin`、`TrueFilterPlugin`、`FakeFilterPlugin` |
| `scheduler/testing/fakehandle/` | `pkg/scheduler/testing/fakehandle/` | `MockUnitFrameworkHandle` |
| K8s Fake Client | `k8s.io/client-go/kubernetes/fake` | `fake.NewSimpleClientset()` + `PrependReactor()` 拦截 API 调用 |
| CRD Fake Client | `godel-scheduler-api/.../fake` | `fake.NewSimpleClientset()` |

### 7.2 测试代码风格（强制）

**所有测试必须使用表驱动模式（Table-Driven Tests）**，与项目现有风格一致：

```go
func TestXxx(t *testing.T) {
    tests := []struct {
        name     string
        // 输入字段
        pod      *v1.Pod
        node     *v1.Node
        // Mock 行为
        cacheSetup func(fc *fakecache.Cache)
        // 期望结果
        wantErr    bool
        wantResult *BindResult
    }{
        {
            name: "正常绑定成功",
            pod:  testing_helper.MakePod().Name("pod-1").Namespace("default").Node("node-1").Obj(),
            // ...
        },
        {
            name:    "节点不存在应返回错误",
            pod:     testing_helper.MakePod().Name("pod-2").Namespace("default").Obj(),
            wantErr: true,
        },
    }
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 测试逻辑
        })
    }
}
```

**断言工具**：
- 使用 `github.com/stretchr/testify/assert` 进行断言
- 复杂结构体比较使用 `github.com/google/go-cmp/cmp`
- 禁止使用 `reflect.DeepEqual`（用 `cmp.Diff` 替代，错误信息更清晰）

### 7.3 覆盖率目标

| 覆盖维度 | 最低要求 |
|----------|---------|
| 新增代码行覆盖率（Line Coverage） | **≥ 80%** |
| 核心路径分支覆盖率（Branch Coverage） | **≥ 90%** |
| 接口契约方法 | **100%**（每个接口方法至少一个正向 + 一个反向用例） |
| 错误路径 | **每个 `return err` 至少一个触发用例** |
| 并发安全 | 涉及 `sync.Mutex` 或 channel 的代码必须有 `-race` 测试 |

**运行方式**：
```bash
# 单包测试
go test -v -race -count=1 ./pkg/binder/...
# 覆盖率报告
go test -coverprofile=cover.out -covermode=atomic ./pkg/binder/...
go tool cover -func=cover.out
```

---

## 8. 各模块单元测试详细规范

### 8.1 模块 A：`binder_interface.go` — Binder 接口定义

**测试文件**：`pkg/binder/binder_interface_test.go`

**接口定义**：
```go
type BinderInterface interface {
    BindUnit(ctx context.Context, req *BindRequest) (*BindResult, error)
    Start(ctx context.Context) error
    Stop()
}

type BindRequest struct {
    Unit        *framework.QueuedUnitInfo
    Pods        []*framework.QueuedPodInfo
    NodeName    string
    Victims     []*v1.Pod
    SchedulerName string
}

type BindResult struct {
    SuccessfulPods []types.UID
    FailedPods     map[types.UID]error
}
```

**必须覆盖的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestBindRequest_Validation_EmptyUnit` | `BindRequest.Unit == nil` 时返回 `ErrInvalidRequest` |
| 2 | `TestBindRequest_Validation_EmptyNodeName` | `BindRequest.NodeName == ""` 时返回错误 |
| 3 | `TestBindRequest_Validation_Valid` | 合法请求通过验证 |
| 4 | `TestBindResult_AllSuccess` | 所有 Pod 绑定成功时 `FailedPods` 为空 |
| 5 | `TestBindResult_PartialFailure` | 部分 Pod 失败时 `SuccessfulPods` 和 `FailedPods` 均有值 |
| 6 | `TestBindResult_AllFailure` | 全部 Pod 失败时 `SuccessfulPods` 为空 |

---

### 8.2 模块 B：`embedded_binder.go` — 内嵌 Binder 实现

**测试文件**：`pkg/binder/embedded_binder_test.go`

**核心结构体**：
```go
type EmbeddedBinder struct {
    binder          *Binder      // 复用现有 Binder 逻辑
    schedulerCache  godelcache.SchedulerCache  // 共享 Cache
    schedulerName   string
    config          *EmbeddedBinderConfig
    running         atomic.Bool
}
```

**必须覆盖的测试用例**：

| # | 测试用例 | 验证点 | Mock 策略 |
|---|---------|--------|----------|
| 1 | `TestEmbeddedBinder_New` | 验证构造函数正确初始化所有字段 | `fake.NewSimpleClientset()` |
| 2 | `TestEmbeddedBinder_New_NilCache` | 传入 nil Cache 时 panic 或返回错误 | - |
| 3 | `TestEmbeddedBinder_Start_Stop` | 启动后 `running=true`，停止后 `running=false` | - |
| 4 | `TestEmbeddedBinder_Start_AlreadyRunning` | 重复启动返回错误 | - |
| 5 | `TestEmbeddedBinder_BindUnit_SinglePod` | 单 Pod 绑定完整流程（冲突检测 → Volume → Bind API） | `fakecache.Cache` + Reactor |
| 6 | `TestEmbeddedBinder_BindUnit_MultiplePods` | 多 Pod Unit（PodGroup）绑定 | `fakecache.Cache` |
| 7 | `TestEmbeddedBinder_BindUnit_ConflictDetected` | 冲突检测失败时正确回退（ForgetPod） | `fakecache.Cache{GetNodeInfoFunc: ...}` |
| 8 | `TestEmbeddedBinder_BindUnit_VolumeBindFailure` | Volume 绑定失败时正确清理 | Mock VolumeBinding 插件 |
| 9 | `TestEmbeddedBinder_BindUnit_APIServerError` | Bind API 调用 409 Conflict 时重试 | `PrependReactor("create", "pods", conflictReactor)` |
| 10 | `TestEmbeddedBinder_BindUnit_Timeout` | 绑定超时时归还资源 | `context.WithTimeout` |
| 11 | `TestEmbeddedBinder_BindUnit_NotRunning` | 未启动时调用返回错误 | - |
| 12 | `TestEmbeddedBinder_BindUnit_WithVictims` | 带抢占 Victims 的绑定流程 | `fakecache.Cache` |
| 13 | `TestEmbeddedBinder_ConcurrentBind` | 并发绑定不 panic、不死锁（用 `go test -race`） | 多 goroutine + `sync.WaitGroup` |

**关键 Mock 模式**（参考现有 `godel_binder_test.go`）：
```go
// 构造内嵌 Binder 测试实例
func newTestEmbeddedBinder(t *testing.T) (*EmbeddedBinder, *fake.Clientset) {
    client := fake.NewSimpleClientset()
    crdClient := crdfake.NewSimpleClientset()
    pCache := &fakecache.Cache{
        AssumeFunc:       func(pod *v1.Pod) error { return nil },
        ForgetFunc:       func(pod *v1.Pod) {},
        IsAssumedPodFunc: func(pod *v1.Pod) bool { return false },
        GetNodeInfoFunc: func(nodeName string) framework.NodeInfo {
            return framework.NewNodeInfo(testing_helper.MakeNode().Name(nodeName).Obj())
        },
    }
    eb := NewEmbeddedBinder(client, crdClient, pCache, "test-scheduler", DefaultEmbeddedBinderConfig())
    return eb, client
}
```

---

### 8.3 模块 C：`cache_adapter.go` — Cache 适配器

**测试文件**：`pkg/binder/cache_adapter_test.go`

**核心结构体**：
```go
// CacheAdapter 将 SchedulerCache 包装为 BinderCache 接口
type CacheAdapter struct {
    schedulerCache godelcache.SchedulerCache
    // BinderCache 特有的额外状态
    assumedPods    *sync.Map  // pod key → *v1.Pod
    podMarkers     *sync.Map  // pod key → preemptorKey
}
```

**必须覆盖的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestCacheAdapter_ImplementsInterface` | 编译期验证 `var _ BinderCache = &CacheAdapter{}` |
| 2 | `TestCacheAdapter_AssumePod` | AssumePod 后 IsAssumedPod 返回 true |
| 3 | `TestCacheAdapter_ForgetPod` | ForgetPod 后 IsAssumedPod 返回 false |
| 4 | `TestCacheAdapter_AssumePod_AlreadyAssumed` | 重复 Assume 同一 Pod 返回错误 |
| 5 | `TestCacheAdapter_GetNodeInfo_Delegation` | 验证请求正确委托给底层 SchedulerCache |
| 6 | `TestCacheAdapter_GetPod_AssumedPod` | 返回 Assumed 态的 Pod（非底层 Cache 的版本） |
| 7 | `TestCacheAdapter_GetPod_NonAssumedPod` | 非 Assumed Pod 委托给底层 SchedulerCache |
| 8 | `TestCacheAdapter_FinishBinding` | 调用后 Pod 从 assumedPods 移除 |
| 9 | `TestCacheAdapter_MarkPodToDelete` | 标记后 IsPodMarkedToDelete 返回 true |
| 10 | `TestCacheAdapter_RemoveDeletePodMarker` | 移除标记后 IsPodMarkedToDelete 返回 false |
| 11 | `TestCacheAdapter_AddPod_UpdatePod_DeletePod` | 事件透传给底层 SchedulerCache |
| 12 | `TestCacheAdapter_AddNode_UpdateNode_DeleteNode` | 节点事件透传 |
| 13 | `TestCacheAdapter_ConcurrentAccess` | 并发 Assume/Forget/GetPod 不 panic（`-race`） |
| 14 | `TestCacheAdapter_GetAvailablePlaceholderPod` | 正确委托 + 返回值校验 |
| 15 | `TestCacheAdapter_UnitSchedulingStatus` | Get/Set UnitSchedulingStatus 往返一致 |

---

### 8.4 模块 D：`node_validator.go` — 绑定前节点验证

**测试文件**：`pkg/binder/node_validator_test.go`

**核心逻辑**：
```go
type NodeValidator struct {
    schedulerName string
    nodeGetter    func(nodeName string) (*v1.Node, error)
}

func (v *NodeValidator) Validate(nodeName string) error {
    node, err := v.nodeGetter(nodeName)
    if err != nil { return err }
    owner := node.Annotations["godel.bytedance.com/scheduler-name"]
    if owner != v.schedulerName {
        return &NodeOwnershipError{Node: nodeName, Expected: v.schedulerName, Actual: owner}
    }
    return nil
}
```

**必须覆盖的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestNodeValidator_Validate_OwnedNode` | 节点归属当前 Scheduler → 通过 |
| 2 | `TestNodeValidator_Validate_OtherSchedulerNode` | 节点归属其他 Scheduler → 返回 `NodeOwnershipError` |
| 3 | `TestNodeValidator_Validate_NoAnnotation` | 节点无 scheduler-name annotation → 返回错误 |
| 4 | `TestNodeValidator_Validate_EmptyAnnotation` | annotation 存在但值为空 → 返回错误 |
| 5 | `TestNodeValidator_Validate_NodeNotFound` | nodeGetter 返回 NotFound → 返回错误 |
| 6 | `TestNodeValidator_Validate_GetterError` | nodeGetter 返回其他错误 → 透传错误 |
| 7 | `TestNodeOwnershipError_ErrorMessage` | 错误信息包含节点名、期望 Scheduler、实际 Scheduler |
| 8 | `TestNodeOwnershipError_IsNodeOwnershipError` | `errors.As` 类型断言可正常工作 |

**构造测试数据**：
```go
node := testing_helper.MakeNode().Name("node-1").Annotation(
    "godel.bytedance.com/scheduler-name", "scheduler-A",
).Obj()
```

---

### 8.5 模块 E：`unit_scheduler.go` — 调度后绑定路径改造

**测试文件**：`pkg/scheduler/core/unit_scheduler/unit_scheduler_test.go`（扩展现有测试）

**改造点**：调度成功后，根据 `embeddedBinder != nil` 决定走直接调用还是 PatchPod 路径。

**必须新增的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestScheduleUnit_EmbeddedBinder_DirectBind` | 内嵌模式下调度成功后直接调用 `BindUnit()`，不触发 `PatchPod` |
| 2 | `TestScheduleUnit_EmbeddedBinder_BindFailure_Retry` | `BindUnit()` 失败后 Pod 正确回退（ForgetPod + 重新入队） |
| 3 | `TestScheduleUnit_EmbeddedBinder_BindFailure_ExceedRetries` | 超过 `MaxLocalRetries` 后触发 `DispatchToAnotherScheduler` |
| 4 | `TestScheduleUnit_LegacyPath_PatchPod` | `embeddedBinder == nil` 时仍走 PatchPod 路径（回归测试） |
| 5 | `TestScheduleUnit_EmbeddedBinder_PodGroup` | PodGroup 多 Pod 走内嵌 Binder 的完整路径 |
| 6 | `TestScheduleUnit_EmbeddedBinder_WithPreemption` | 带 Victims 的调度结果传递给 Binder |

**Mock 策略**（参考现有 `unit_scheduler_test.go` 的 `mockScheduler`）：
```go
// 新增 mockEmbeddedBinder
type mockEmbeddedBinder struct {
    bindFunc func(ctx context.Context, req *BindRequest) (*BindResult, error)
    started  bool
}

func (m *mockEmbeddedBinder) BindUnit(ctx context.Context, req *BindRequest) (*BindResult, error) {
    if m.bindFunc != nil {
        return m.bindFunc(ctx, req)
    }
    return &BindResult{SuccessfulPods: []types.UID{req.Pods[0].Pod.UID}}, nil
}
```

**验证 PatchPod 不被调用**：
```go
// 使用 Reactor 拦截验证
patchCalled := false
client.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
    patchCalled = true
    return true, nil, nil
})
// ... 执行调度 ...
assert.False(t, patchCalled, "embedded binder mode should NOT call PatchPod")
```

---

### 8.6 模块 F：`pkg/binder/utils/util.go` — CleanupPodAnnotations 增强

**测试文件**：`pkg/binder/utils/util_test.go`（新建，当前缺失）

**改造点**：增加 `binderRetryCount` 判断，超过阈值时回退 Dispatcher 而非同一 Scheduler。

**必须覆盖的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestCleanupPodAnnotations_ResetToDispatched` | 默认行为：状态重置为 `PodDispatched`（回归测试） |
| 2 | `TestCleanupPodAnnotations_ClearsAllSchedulingAnnotations` | 验证 AssumedNode、NominatedNode 等 6 个 annotation 被清除 |
| 3 | `TestCleanupPodAnnotations_PatchFailure` | PatchPod 失败时返回错误，metrics 记录 failure |
| 4 | `TestCleanupPodAnnotations_NilAnnotations` | Pod.Annotations 为 nil 时不 panic |
| 5 | `TestCleanupPodAnnotationsWithRetryCount_BelowThreshold` | 重试次数 < 阈值 → 回退同一 Scheduler（`PodDispatched`） |
| 6 | `TestCleanupPodAnnotationsWithRetryCount_AboveThreshold` | 重试次数 ≥ 阈值 → 回退 Dispatcher（`PodPending` + 清除 SchedulerAnnotation） |
| 7 | `TestCleanupPodAnnotationsWithRetryCount_ExactThreshold` | 边界值：恰好等于阈值时回退 Dispatcher |
| 8 | `TestCleanupPodAnnotationsWithRetryCount_PreservesFailedSchedulers` | 回退 Dispatcher 时保留 `FailedSchedulersAnnotationKey` |

**Mock 策略**：
```go
client := fake.NewSimpleClientset(pod)
// 验证 Patch 内容
client.PrependReactor("patch", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
    patchAction := action.(k8stesting.PatchAction)
    patchBytes := patchAction.GetPatch()
    // 解析 patch JSON，验证 annotation 变更
    var patch map[string]interface{}
    json.Unmarshal(patchBytes, &patch)
    // 断言 ...
    return false, nil, nil // 继续执行默认逻辑
})
```

---

### 8.7 模块 G：`pkg/binder/utils/retry.go` — 重试与失败计数

**测试文件**：`pkg/binder/utils/retry_test.go`

**核心逻辑**：
```go
func GetBindFailureCount(pod *v1.Pod) int
func SetBindFailureCount(pod *v1.Pod, count int) *v1.Pod
func IncrementBindFailureCount(pod *v1.Pod) *v1.Pod
func ShouldDispatchToAnotherScheduler(pod *v1.Pod, maxRetries int) bool
func SetLastBindFailureReason(pod *v1.Pod, reason string) *v1.Pod
func GetLastBindFailureReason(pod *v1.Pod) string
```

**必须覆盖的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestGetBindFailureCount_NoAnnotation` | 无 annotation 时返回 0 |
| 2 | `TestGetBindFailureCount_ValidValue` | 正常解析 annotation 值 |
| 3 | `TestGetBindFailureCount_InvalidValue` | 非数字 annotation 返回 0（容错） |
| 4 | `TestSetBindFailureCount_SetsAnnotation` | 正确写入 annotation |
| 5 | `TestSetBindFailureCount_NilAnnotations` | Pod.Annotations 为 nil 时自动初始化 |
| 6 | `TestIncrementBindFailureCount_FromZero` | 0 → 1 |
| 7 | `TestIncrementBindFailureCount_Increment` | N → N+1 |
| 8 | `TestShouldDispatchToAnotherScheduler_BelowMax` | 未达阈值 → false |
| 9 | `TestShouldDispatchToAnotherScheduler_AtMax` | 恰好达到阈值 → true |
| 10 | `TestShouldDispatchToAnotherScheduler_AboveMax` | 超过阈值 → true |
| 11 | `TestSetLastBindFailureReason` | 正确写入原因 annotation |
| 12 | `TestGetLastBindFailureReason_NoAnnotation` | 无 annotation 返回空字符串 |

---

### 8.8 模块 H：`scheduler.go` — Scheduler 集成内嵌 Binder

**测试文件**：`pkg/scheduler/scheduler_test.go`（扩展现有测试）

**改造点**：Scheduler 结构体新增 `embeddedBinder BinderInterface` 字段，在 `Run()` 中启动 / `Close()` 中停止。

**必须新增的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestScheduler_WithEmbeddedBinder_Init` | 配置 `EnableEmbeddedBinder=true` 时正确创建 Binder |
| 2 | `TestScheduler_WithoutEmbeddedBinder_Init` | 配置 `EnableEmbeddedBinder=false` 时 `embeddedBinder == nil` |
| 3 | `TestScheduler_Run_StartsEmbeddedBinder` | `Run()` 时内嵌 Binder 的 `Start()` 被调用 |
| 4 | `TestScheduler_Close_StopsEmbeddedBinder` | `Close()` 时内嵌 Binder 的 `Stop()` 被调用 |
| 5 | `TestScheduler_EmbeddedBinder_SharesCache` | 验证 Binder 和 Scheduler 使用同一 Cache 引用 |

---

### 8.9 模块 I：`eventhandlers.go` — 内嵌模式下的事件过滤

**测试文件**：`pkg/binder/eventhandlers_test.go`（扩展现有测试）

**改造点**：内嵌模式下，新到达的 Pod 不再通过 Informer 路径进入 BinderQueue，而是由 Scheduler 直接推入。

**必须新增的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestAddPodToBinderQueue_EmbeddedMode_Skip` | 内嵌模式下 `addPodToBinderQueue()` 对 AssumedPod 不重复入队 |
| 2 | `TestAddPodToBinderQueue_StandaloneMode_Normal` | 独立模式下行为不变（回归测试） |
| 3 | `TestUpdatePodInBinderQueue_EmbeddedMode` | 内嵌模式下 Update 事件依然正确处理已入队 Pod 的更新 |
| 4 | `TestDeletePodFromBinderQueue_EmbeddedMode` | 内嵌模式下 Pod 删除仍触发 Cache 清理 |

---

### 8.10 模块 J：`binder_reconciler.go` — Reconciler 增强

**测试文件**：`pkg/binder/binder_reconciler_test.go`（新建，当前缺失）

**必须覆盖的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestReconciler_AddFailedTask_Enqueue` | 添加失败任务后队列长度 +1 |
| 2 | `TestReconciler_Worker_RejectFailed_Success` | Reject API 成功时任务出队 |
| 3 | `TestReconciler_Worker_RejectFailed_TransientError` | 临时错误时重新入队（指数退避） |
| 4 | `TestReconciler_Worker_RejectFailed_NotFound` | Pod 已不存在时忽略（不重试） |
| 5 | `TestReconciler_Run_Stop` | 启动后能正常处理，Stop 后优雅退出 |
| 6 | `TestReconciler_ConcurrentAdd` | 多 goroutine 并发 AddFailedTask 不 panic |

---

### 8.11 模块 K：`EmbeddedBinderConfig` — 配置与默认值

**测试文件**：`pkg/binder/embedded_binder_config_test.go`

**必须覆盖的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestDefaultEmbeddedBinderConfig` | 默认值正确（MaxBindRetries=3, BindTimeout=30s, MaxLocalRetries=5） |
| 2 | `TestEmbeddedBinderConfig_Validate_Valid` | 合法配置通过验证 |
| 3 | `TestEmbeddedBinderConfig_Validate_NegativeRetries` | MaxBindRetries < 0 时返回错误 |
| 4 | `TestEmbeddedBinderConfig_Validate_ZeroTimeout` | BindTimeout = 0 时返回错误 |
| 5 | `TestEmbeddedBinderConfig_Validate_MaxLocalRetriesZero` | MaxLocalRetries = 0（禁用回退）时合法 |

---

### 8.12 模块 L：Options 扩展

**测试文件**：`pkg/scheduler/options_test.go`（扩展现有测试）或 `cmd/scheduler/app/options/options_test.go`

**必须新增的测试用例**：

| # | 测试用例 | 验证点 |
|---|---------|--------|
| 1 | `TestOptions_EnableEmbeddedBinder_Default` | 默认关闭（`false`） |
| 2 | `TestOptions_EnableEmbeddedBinder_Flag` | `--enable-embedded-binder=true` 正确解析 |
| 3 | `TestOptions_BinderConfig_Flags` | `--max-bind-retries`、`--bind-timeout`、`--max-local-retries` 正确解析 |
| 4 | `TestOptions_BinderConfig_InvalidValues` | 非法值被 Validate 拒绝 |

---

## 9. 集成测试与性能基准测试

### 9.1 集成测试

**测试文件**：`test/e2e/embedded_binder_test.go` 或 `pkg/binder/integration_test.go`（使用 `// +build integration` tag）

| # | 测试场景 | 验证点 |
|---|---------|--------|
| 1 | 单 Scheduler + 内嵌 Binder 端到端绑定 | Pod 从 Pending → Dispatched → Assumed → Bound 全流程 |
| 2 | 配置开关切换 | `EnableEmbeddedBinder=true/false` 热切换后行为正确 |
| 3 | 多 Pod PodGroup 并行绑定 | minMember=3 的 PodGroup 全部绑定成功 |
| 4 | 绑定失败后回退 Dispatcher | 超过 MaxLocalRetries 后 Pod 回退到 Pending 状态 |
| 5 | 节点重分区期间的绑定行为 | 节点 annotation 变更后，NodeValidator 阻止绑定 |
| 6 | 功能等价性：内嵌 vs 独立 Binder 相同输入产生相同结果 | 对同一组 Pod+Node 输入，两种模式的绑定结果一致 |

### 9.2 性能基准测试

**测试文件**：`pkg/binder/benchmark_test.go`

```go
func BenchmarkEmbeddedBinder_BindUnit(b *testing.B) {
    eb, _ := newTestEmbeddedBinder(b)
    eb.Start(context.Background())
    defer eb.Stop()
    
    req := &BindRequest{
        Unit:     makeTestUnit(1),
        Pods:     makeTestPods(1),
        NodeName: "node-1",
    }
    
    b.ResetTimer()
    b.RunParallel(func(pb *testing.PB) {
        for pb.Next() {
            eb.BindUnit(context.Background(), req)
        }
    })
}

func BenchmarkEmbeddedBinder_BindUnit_PodGroup(b *testing.B) {
    // minMember=5 的 PodGroup 绑定性能
}

func BenchmarkCacheAdapter_AssumePod(b *testing.B) {
    // Cache 适配器的 Assume 性能
}

func BenchmarkNodeValidator_Validate(b *testing.B) {
    // 节点验证性能（应在 100ns 以内）
}
```

**性能目标**（论文数据）：

| 指标 | 共享 Binder 基线 | 独立 Binder 目标 |
|------|----------------|-----------------|
| 单 Pod 绑定延迟 (P99) | 测量值 | ≤ 50% 基线 |
| 绑定吞吐量 (pods/s/scheduler) | 测量值 | ≥ 基线 |
| N Scheduler 线性扩展比 | 1.0 | ≥ 0.85×N |
| Cache 同步延迟 | 秒级（Informer） | < 1ms（共享内存） |

---

## 10. 关键注意事项

### 10.1 绝对不能改动的

- Gödel CRD API（PodGroup, Scheduler, Movement 等）
- Dispatcher 的调度单元分发逻辑
- NodeShuffler 的节点分区逻辑
- Pod 的 annotation 键名（`godel.bytedance.com/*`）

### 10.2 必须保持的行为等价性

- 绑定结果必须与共享 Binder 完全一致
- PodGroup 状态机转换不变
- 冲突检测逻辑不变（CheckTopology, CheckConflicts）
- Volume 绑定逻辑不变

### 10.3 已知的坑

1. **BinderCache 的 PodAssumedTTL**（当前 5 分钟）：内嵌模式下共享 Cache 后，TTL 语义需要重新评估
2. **Reconciler**（`BinderTasksReconciler`）：负责清理过期的绑定任务，内嵌后需要确保仍然运行
3. **Binder 的 leader-elect 当前是 false**：内嵌后不再需要独立的 leader 选举
4. **`SchedulerName` 字段**：Binder 使用 `SchedulerName` 过滤 Pod（`AssumedPodOfGodel`），内嵌后这个过滤逻辑需要与 Scheduler 保持一致

---

## 11. 容错机制：失败回退 Dispatcher 重分配

### 11.1 当前容错链路分析

当前 Gödel 已有两层容错机制，但在独立 Binder 架构下需要增强：

#### 11.1.1 Scheduler 调度失败 → 回退 Dispatcher

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

#### 11.1.2 Binder 绑定失败 → 回退 Scheduler

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

### 11.2 当前机制的不足

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

### 11.3 独立 Binder 架构必须增强的容错

#### 11.3.1 多次调度/绑定失败 → 回退 Dispatcher

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

#### 11.3.2 Scheduler+Binder 进程健康检测

**设计**：Dispatcher 的 SchedulerMaintainer 已有 Scheduler 心跳检测机制。独立 Binder 架构下，Scheduler 进程故障等同于 Binder 故障，Dispatcher 需要：

1. 检测到 Scheduler 不可用后，将其节点分区重新分配
2. 将该 Scheduler 上所有 Dispatched/Assumed 状态的 Pod 回收，重置为 Pending
3. 通过 `FailedSchedulersAnnotationKey` 避免再次分配到同一（恢复后的）Scheduler

#### 11.3.3 Binder 内部重试与熔断

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

#### 11.3.4 失败信息传播

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

### 11.4 容错状态机

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

### 11.5 实施优先级

| 任务 | 优先级 | 阶段 |
|------|--------|------|
| Scheduler 多次失败回退 Dispatcher（`MaxLocalRetries`） | **P0** | Phase 2 |
| Binder 失败回退 Dispatcher（而非同一 Scheduler） | **P0** | Phase 2 |
| 失败信息 Annotation 传播 | **P1** | Phase 2 |
| Dispatcher 感知 Scheduler 故障并回收 Pod | **P1** | Phase 3 |
| Binder 熔断机制 | **P2** | Phase 4 |
| 失败 metrics 与告警 | **P2** | Phase 5 |

---

## 12. 测试检查清单（PR 合并前必须全部通过）

### 12.1 单元测试检查清单

- [ ] 所有新增 `_test.go` 文件均使用表驱动模式
- [ ] 所有测试通过 `go test -race` 无数据竞争
- [ ] `pkg/binder/binder_interface_test.go`：≥ 6 个用例 ✅
- [ ] `pkg/binder/embedded_binder_test.go`：≥ 13 个用例 ✅
- [ ] `pkg/binder/cache_adapter_test.go`：≥ 15 个用例 ✅
- [ ] `pkg/binder/node_validator_test.go`：≥ 8 个用例 ✅
- [ ] `pkg/scheduler/core/unit_scheduler/unit_scheduler_test.go`：新增 ≥ 6 个用例 ✅
- [ ] `pkg/binder/utils/util_test.go`：≥ 8 个用例 ✅
- [ ] `pkg/binder/utils/retry_test.go`：≥ 12 个用例 ✅
- [ ] `pkg/scheduler/scheduler_test.go`：新增 ≥ 5 个用例 ✅
- [ ] `pkg/binder/eventhandlers_test.go`：新增 ≥ 4 个用例 ✅
- [ ] `pkg/binder/binder_reconciler_test.go`：≥ 6 个用例 ✅
- [ ] `pkg/binder/embedded_binder_config_test.go`：≥ 5 个用例 ✅
- [ ] `pkg/scheduler/options_test.go` 或 `cmd/scheduler/app/options/options_test.go`：新增 ≥ 4 个用例 ✅

### 12.2 代码质量检查清单

- [ ] `go vet ./pkg/binder/... ./pkg/scheduler/...` 无告警
- [ ] `golint` / `golangci-lint` 无新增告警
- [ ] 所有新增 exported 函数有 GoDoc 注释
- [ ] 新增 metrics 有对应的单元测试验证注册和标签
- [ ] `go test -coverprofile` 新增代码覆盖率 ≥ 80%

### 12.3 集成测试检查清单

- [ ] 单 Scheduler + 内嵌 Binder 端到端通过
- [ ] 配置开关回退到独立 Binder 模式正常工作
- [ ] PodGroup 绑定功能等价性验证通过
- [ ] 性能基准测试数据已收集并记录

### 12.4 每个 Phase 的测试门槛

| Phase | 必须通过的测试 |
|-------|--------------|
| Phase 1 | 模块 A（接口）+ 模块 K（配置）+ 模块 L（Options） |
| Phase 2 | 模块 B（内嵌 Binder）+ 模块 C（Cache 适配器）+ 模块 E（调度路径）+ 模块 F/G（回退逻辑） |
| Phase 3 | 模块 I（事件处理）+ PodGroupController 分区过滤测试 |
| Phase 4 | 模块 D（节点验证）+ 模块 J（Reconciler）+ 容错集成测试 |
| Phase 5 | 全量集成测试 + 性能基准测试 + 功能等价性测试 |