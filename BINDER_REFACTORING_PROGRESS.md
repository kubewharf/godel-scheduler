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

**状态**：⬜ 未开始

---

## Phase 4：容错与节点验证

**状态**：⬜ 未开始

---

## Phase 5：部署与测试

**状态**：⬜ 未开始
