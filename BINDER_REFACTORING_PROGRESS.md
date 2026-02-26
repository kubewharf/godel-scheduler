# 独立 Binder 架构改造进度记录

## Phase 1：接口定义与配置开关

**状态**：✅ 已完成  
**完成日期**：2026-02-26

### 新增文件

| 文件 | 用途 |
|------|------|
| `pkg/binder/binder_interface.go` | `BinderInterface` 接口 + `BindRequest` / `BindResult` 结构体 + 验证逻辑 |
| `pkg/binder/embedded_binder_config.go` | `EmbeddedBinderConfig` 配置结构体 + 默认值 + 验证 |
| `pkg/binder/binder_interface_test.go` | 接口测试（模块 A）— 8 个测试函数，覆盖验证正向/反向用例 |
| `pkg/binder/embedded_binder_config_test.go` | 配置测试（模块 K）— 2 个测试函数，10 个子用例 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `cmd/scheduler/app/options/options.go` | `Options` 新增 `EnableEmbeddedBinder` + `EmbeddedBinderConfig` 字段；注册 `--enable-embedded-binder`、`--max-bind-retries`、`--bind-timeout`、`--max-local-retries` CLI flags；`Validate()` 中条件校验 |
| `cmd/scheduler/app/options/options_test.go` | 新增 4 个测试函数（模块 L）：默认值、flag 解析、自定义值、非法值校验 |

### Phase 1 交付物对照

- [x] 定义 `BinderInterface`（`BindUnit` / `Start` / `Stop`）
- [x] 定义 `BindRequest` / `BindResult` 结构体（含 `Validate()`）
- [x] `--enable-embedded-binder` 开关（默认 `false`，向后兼容）
- [x] `EmbeddedBinderConfig` 及默认值（`MaxBindRetries=3`, `BindTimeout=30s`, `MaxLocalRetries=5`）
- [x] 模块 A 测试 ≥ 6 个用例 ✓（8 个）
- [x] 模块 K 测试 ≥ 5 个用例 ✓（10 个子用例）
- [x] 模块 L 测试 ≥ 4 个用例 ✓（4 个测试函数，14 个子用例）

### 测试结果

- 全部 PASS，含 `-race` 检测
- `go vet` 无新增告警
- 所有现有测试回归通过

---

## Phase 2：核心集成

**状态**：✅ 已完成  
**完成日期**：2026-02-26

### 新增文件

| 文件 | 用途 | 测试数 |
|------|------|--------|
| `pkg/binder/utils/retry.go` | 绑定失败计数 + Dispatcher 重调度判定（模块 B） | — |
| `pkg/binder/utils/retry_test.go` | retry 逻辑测试 | 6 个函数，19 个子用例 |
| `pkg/binder/utils/util_test.go` | `CleanupPodAnnotations` / `CleanupPodAnnotationsWithRetryCount` 测试 | 8 个函数 |
| `pkg/binder/cache_adapter.go` | `CacheAdapter`：包装 SchedulerCache 为 BinderCache（模块 C） | — |
| `pkg/binder/cache_adapter_test.go` | CacheAdapter 测试，含并发竞态测试 | 18 个函数 |
| `pkg/binder/embedded_binder.go` | `EmbeddedBinder`：实现 `BinderInterface`（模块 E） | — |
| `pkg/binder/embedded_binder_test.go` | EmbeddedBinder 测试，含冲突重试、超时、并发 | 16 个函数 |
| `pkg/scheduler/embedded_binder_integration_test.go` | Scheduler 层嵌入式 Binder 集成测试 | 4 个函数 |
| `pkg/scheduler/core/unit_scheduler/embedded_binder_test.go` | UnitScheduler 嵌入式绑定路径测试 | 7 个函数 |

### 修改文件

| 文件 | 改动 |
|------|------|
| `pkg/binder/utils/util.go` | 提取 `cleanupSchedulingAnnotations()` 辅助函数；新增 `CleanupPodAnnotationsWithRetryCount()` 含重试计数感知的 Dispatcher 回退逻辑 |
| `pkg/scheduler/scheduler.go` | 新增 `embeddedBinder` 字段；`SetEmbeddedBinder`/`GetEmbeddedBinder`/`GetCache` 方法；`Run()` 中启动/停止嵌入式 Binder 生命周期管理；`createDataSet()` 中向 UnitScheduler 传播 Binder |
| `pkg/scheduler/core/types.go` | `UnitScheduler` 接口新增 `SetEmbeddedBinder`/`GetEmbeddedBinder` 方法 |
| `pkg/scheduler/core/unit_scheduler/unit_scheduler.go` | 新增 `embeddedBinder` 字段及 setter/getter；将 `PersistSuccessfulPods` 拆分为 `persistViaEmbeddedBinder`（直接绑定路径）和 `persistViaPatchPod`（传统 Patch 路径） |
| `cmd/scheduler/app/config/config.go` | `Config` 新增 `EnableEmbeddedBinder` 和 `EmbeddedBinderConfig` 字段 |
| `cmd/scheduler/app/options/options.go` | `ApplyTo()` 中传播嵌入式 Binder 配置到 Config |
| `cmd/scheduler/app/server.go` | `Run()` 中按配置创建 `EmbeddedBinder` 并注入到 Scheduler |

### Phase 2 交付物对照

- [x] **模块 B**：`retry.go` — 绑定失败计数（`Get/Set/IncrementBindFailureCount`）+ Dispatcher 重调度判定（`ShouldDispatchToAnotherScheduler`）+ 失败原因记录
- [x] **模块 C**：`cache_adapter.go` — `CacheAdapter` 将 SchedulerCache 包装为 BinderCache 接口（`AssumePod`/`ForgetPod`/`FinishBinding`/`MarkPodToDelete` 等）
- [x] **模块 E**：`embedded_binder.go` — 完整实现 `BinderInterface`（`BindUnit` 含 Conflict/Timeout 指数退避重试；`Start`/`Stop` 原子生命周期管理）
- [x] **模块 F**：`scheduler.go` 集成 — Scheduler 持有并管理 EmbeddedBinder 生命周期；`createDataSet` 自动传播到 UnitScheduler
- [x] **模块 G**：`unit_scheduler.go` 绑定路径修改 — `PersistSuccessfulPods` 根据 `embeddedBinder` 是否存在分流到直接绑定 / 传统 Patch 路径
- [x] **模块 H**：`server.go` + `config.go` + `options.go` 端到端串联 — CLI 参数 → Config → Server → Scheduler → UnitScheduler 完整链路
- [x] `util.go` 增强 — `CleanupPodAnnotationsWithRetryCount` 支持按重试次数回退到 Dispatcher
- [x] 全部新增代码测试覆盖 — 共 59 个新测试函数/用例，全部 PASS（含 `-race`）

### 测试结果

- 新增测试全部 PASS（`go test -race -count=1`）
- 全量构建通过：`go build ./cmd/scheduler/... ./cmd/binder/...`
- 现有测试中 `TestSchedulerEvent`、`TestPrepareNodes` 等少量失败为 **预存问题**（已在 `git stash` 对比中确认与本次改动无关）

---

## Phase 3：PodGroupController 迁移

**状态**：✅ 已完成  
**完成日期**：2026-02-26

### 修改文件

| 文件 | 改动 |
|------|------|
| `pkg/binder/controller/podgroup.go` | 新增 `PodGroupControllerOptions` 结构体（含 `SchedulerName` 字段）；新增 `SetupPodGroupControllerWithOptions` 入口（嵌入模式使用）；保留 `SetupPodGroupController` 向后兼容；新增 `podBelongsToPartition` 分区过滤方法（按 `SchedulerAnnotationKey` 过滤 Pod）；`syncHandler` 中在计算 PodGroup 状态前按分区过滤 Pod；`updatePodGroup` 中增加 `IsConflict` 冲突重试处理（annotation 和 status 更新均支持） |
| `cmd/scheduler/app/server.go` | 当 `EnableEmbeddedBinder` 为 true 时，在 `run()` 中使用 `SetupPodGroupControllerWithOptions` 启动 PodGroupController，传入 `SchedulerName` 实现分区隔离 |
| `pkg/binder/eventhandlers.go` | `addAllEventHandlers` 增加 `embeddedMode bool` 参数；嵌入模式下跳过 BinderQueue 相关的 informer handler 注册（`addPodToBinderQueue`、`updatePodInBinderQueue`、`deletePodFromBinderQueue`）；Cache handler 始终注册 |
| `pkg/binder/godel_binder.go` | 调用 `addAllEventHandlers` 时传入 `false`（独立模式适配） |

### 新增测试文件

| 文件 | 用途 | 测试数 |
|------|------|--------|
| `pkg/binder/controller/podgroup_partition_test.go` | PodGroupController 分区过滤测试 | 8 个测试（含 6 个 `podBelongsToPartition` 子用例 + 分区过滤场景覆盖 + 向后兼容性验证） |
| `pkg/binder/eventhandlers_embedded_test.go` | 嵌入模式 EventHandler 行为测试 | 6 个测试（嵌入模式跳过、独立模式正常、Update/Delete handler、非 Assumed Pod 过滤、错误 Scheduler 过滤） |

### Phase 3 交付物对照

- [x] **PodGroupController 提取**：`SetupPodGroupControllerWithOptions` 新入口，支持通过 `PodGroupControllerOptions` 传入调度器名称
- [x] **分区过滤**：`podBelongsToPartition` 方法按 `SchedulerAnnotationKey` 注解过滤 Pod，`syncHandler` 仅统计属于本分区的 Pod
- [x] **多实例冲突处理**：`updatePodGroup` 中 annotation 和 status 更新均增加 `apierrs.IsConflict` 冲突重试（RefreshGet + Retry）
- [x] **Scheduler 集成**：`cmd/scheduler/app/server.go` 在 `EnableEmbeddedBinder` 时启动 PodGroupController，使用 `SchedulerName` 分区
- [x] **EventHandler 嵌入模式跳过**：`addAllEventHandlers` 在嵌入模式下不注册 BinderQueue handler，避免与 Scheduler 调度队列冲突
- [x] **向后兼容**：`SetupPodGroupController` 保持不变，独立 Binder 模式行为无改动

### 测试结果

- `pkg/binder/...` + `pkg/binder/controller/...`：**全部通过**（含 `-race` 竞态检测）
- `go vet` 无新增告警（`pkg/binder/controller/...`、`cmd/scheduler/...` 均通过）
- 编译验证：`cmd/scheduler`、`cmd/binder` 均正常构建
- 原有 21 个 PodGroupController 测试全部回归通过

---

## Phase 4：容错与节点验证

**状态**：✅ 已完成  
**完成日期**：2026-02-27

### 新增文件

| 文件 | 用途 | 测试数 |
|------|------|--------|
| `pkg/binder/node_validator.go` | `NodeValidator`：绑定前验证目标节点仍属于当前 Scheduler 分区（模块 D）；`NodeGetter` 类型 + `NodeOwnershipError` 类型化错误 + `IsNodeOwnershipError()` 辅助函数 | — |
| `pkg/binder/node_validator_test.go` | NodeValidator 测试 | 12 个测试（8 个 Validate 子用例 + NilGetter + ErrorMessage + IsNodeOwnershipError 4 个子用例） |
| `pkg/binder/binder_reconciler_test.go` | BinderTasksReconciler 增强测试 | 9 个测试（Enqueue、Success、TransientError、NotFound、Run/Stop、ConcurrentAdd、DispatcherFallback、LocalRetry、Len） |
| `pkg/binder/metrics/embedded_binder_metrics.go` | 嵌入式 Binder 专用指标：`nodeValidationFailures`、`embeddedBinderBindTotal`、`embeddedBinderBindLatency`、`dispatcherFallbackTotal` | — |

### 修改文件

| 文件 | 改动 |
|------|------|
| `pkg/binder/binder_reconciler.go` | 新增 `schedulerName` / `maxLocalRetries` 字段；新增 `NewBinderTaskReconcilerWithRetry()` 重试感知构造函数；新增 `Len()` 方法；新增 `cleanupPod()` 方法（按配置分派到 `CleanupPodAnnotations` 或 `CleanupPodAnnotationsWithRetryCount`）；`APICallFailedWorker` 使用 `cleanupPod()` |
| `pkg/binder/embedded_binder.go` | 新增 `nodeValidator` / `reconciler` 字段；`NewEmbeddedBinder` 增加 `nodeGetter NodeGetter` 参数（第 6 参数）；`BindUnit()` 绑定前执行节点验证步骤；`Start()` / `Stop()` 管理 reconciler 生命周期；新增 `bindermetrics` 导入 |
| `pkg/binder/embedded_binder_test.go` | 现有 3 处 `NewEmbeddedBinder()` 调用增加第 6 参数 `nil`；新增 3 个 NodeValidator 集成测试 |
| `cmd/scheduler/app/server.go` | 新增 `v1` 导入；通过 Node Informer Lister 构建 `NodeGetter` 并传入 `NewEmbeddedBinder` |

### Phase 4 交付物对照

- [x] **模块 D**：`NodeValidator` — 通过 `GodelSchedulerNodeAnnotationKey` 注解验证节点归属；支持 nil NodeGetter 跳过验证（向后兼容）
- [x] **`NodeOwnershipError`**：类型化错误，支持 `errors.As` 解包；`IsNodeOwnershipError()` 辅助函数
- [x] **模块 J**：`BinderTasksReconciler` 增强 — `NewBinderTaskReconcilerWithRetry` 支持 `maxLocalRetries` 配置；`cleanupPod` 按重试次数选择清理策略；`Len()` 队列长度检查
- [x] **绑定指标**：4 个新增 Prometheus 指标（`node_validation_failures`、`embedded_binder_bind_total`、`embedded_binder_bind_latency`、`dispatcher_fallback_total`）
- [x] **EmbeddedBinder 集成**：`BindUnit()` 在绑定前执行节点验证；验证失败直接返回错误，不进入绑定流程
- [x] **Scheduler 集成**：`server.go` 通过 Node Informer Lister 提供 `NodeGetter`

### 测试结果

- NodeValidator 测试：12 个，全部 PASS
- Reconciler 测试：9 个，全部 PASS
- NodeValidator 集成测试：3 个（OwnedNode / OtherNode / Disabled），全部 PASS
- `pkg/binder/...` 全量测试：全部 PASS（含 `-race` 竞态检测）
- `go vet` 无新增告警
- 编译验证：`cmd/scheduler`、`cmd/binder` 均正常构建

---

## Phase 5：部署与测试

**状态**：⬜ 未开始
