# Brunel — 當前狀態

> 最後同步：2026-09-17
> Branch：main
> Working tree：乾淨（與 `origin/main` 同步）

## 架構轉向

- [ADR-002](docs/adr/ADR-002-pi-agent-runtime.md)（2026-08-12）：放棄零依賴單檔 exe 需求（ADR-001 硬需求 (a)、spec.md 原 G-1），改採 Pi 作為 model-facing Agent Runtime（Route B）；ADR-001 部分 Superseded，Job Object／Workspace／Safety／PowerShell 執行器與 Go 1.25.x／Bubble Tea v2 基線仍維持 Go 實作。依據 [#24 Pi Compatibility Spike](https://github.com/bext1998/brunel/issues/24) 的 Gate 1/3/4 Pass、Gate 2 Pass（有但書）、Gate 0 Partial；Spike 分支 `agent/pi-spike-issue-24` 已 push 至遠端（不合併）。
- `docs/spec.md` 已同步修訂至 **v1.3**：8 個工具全留 Go（經 `taylor-tools.ts` extension 暴露給 Pi）、Session 以 Brunel 自己的 `events.jsonl` 為準（Pi session 停用）、Provider 開放多家（不再限定 OpenRouter，交由 Pi 生態決定）。新增 INV-9（`internal/pirpc` 禁止送出 `bash` RPC command）、OQ-8／OQ-9。
- 架構轉向已拆解為 Issues（2026-09-08）：#8（F-7）、#9（F-8）已依 v1.3 §4 矩陣改標題與範圍；新增 [#29](https://github.com/bext1998/brunel/issues/29)（`taylor-tools.ts` extension 與 `brunel.exe --taylor-tool` 派工，由 #9 拆出）、[#30](https://github.com/bext1998/brunel/issues/30)（INV-9 `bash` command 禁令與 CI lint／AST 檢查，由 #9 拆出），皆為 #1 的 sub-issue。#9 相依改為 Blocked by #4／#8／#29／#30。
- Gate 0（物理上無 Git Bash 的環境）補測依使用者裁決不另建 Issue，維持 ADR-002 現況：Git for Windows 為已文件化安裝依賴，spec OQ-9 視為接受風險、不驗證。OQ-8（Pi 版本釘選政策）尚未建 Issue。

## 進行中 Issues

- [#1 Alpha 1：薄型 coding harness 實作追蹤](https://github.com/bext1998/brunel/issues/1) 已依 v1.2 對齊；未完成子項為 #2、#4、#7、#9、#11、#14、#22、#29（#30 已於 PR #36 完成並 CLOSED）。#8、#9 已依 ADR-002／v1.3 重新拆解完成（見「架構轉向」）；#4／#8／#9 核心實作已合併（見下）。
- [#4 F-3：8 個固定內建工具與凍結 schema](https://github.com/bext1998/brunel/issues/4) 的 `internal/tools` 核心實作已透過 PR #33 合併至 `main`：固定 registry、嚴格 JSON／必填欄位驗證（`DisallowUnknownFields`、拒 `null`／尾隨 JSON、無自動補值）、8 工具的結構化 `Result`，以及每個 I/O 路徑在 workspace resolve、`filetools`、Git 或 PowerShell 前經真實 `safety.Gate.Decide`（INV-1）。新增 `list_files`、`search_text`、Git-only `workspace_diff`；其餘工具接上既有 `filetools`／`exec`。經兩個隔離 Sonnet subagent 審查並修正兩個 major（INV-1 no-bypass 測試補齊 8 工具的 `Registry{Gate:nil}` 反例；`tools.ErrorCode` 串接 `safety`／`workspace`／`filetools`／`exec` 的 `ErrorCode`）。TC-WS／TC-FILE／TC-SAFE 覆蓋與 Windows AC-6 閉環測試通過；CI `windows-latest`＋`ubuntu-latest` 綠。殘留 minor／nit（`workspace_diff` timeout 混碼與漏 staged／untracked、`search_text` 無上限、`max_depth:0` 未文件化、巢狀 null coerce 等）記於 [#4 review 註解](https://github.com/bext1998/brunel/issues/4#issuecomment-5620453317)。`--taylor-tool` 進入點與 `taylor-tools.ts` 已由 PR #37（#29 核心）合併；Pi bridge 已由 PR #40（#9 核心）合併，見下方 #9 條目。
- [#5 F-4：實作 stale-read hash 防護與原子寫入](https://github.com/bext1998/brunel/issues/5) 核心實作已透過 PR #26 合併至 `main`（`internal/filetools`）：全檔 SHA-256、`create_file`／`write_file`／`apply_patch` 的 `expected_hash` 前置條件、精確 hunk 套用（無自動 merge／模糊比對）、暫存檔＋鎖內重驗＋原子換檔；stale hash、patch conflict、overlap、缺失目標、失敗寫入與鎖衝突皆保留原檔且有界失敗。#4（PR #33）已將它接至 `workspace.Workspace` 與 §5.4 工具呼叫路徑，並經 PR #37 的 `brunel --taylor-tool` 進入點可從子行程呼叫；#9 核心（PR #40）已合併，但真實 `pi --mode rpc` 端到端驗證仍未跑（僅 fakepi 子行程與單元測試）。INV-6 為 best-effort（見 spec §10／OQ-10）。
- [#7 F-6：實作 AUTO／CONFIRM 事故防護與 Approver](https://github.com/bext1998/brunel/issues/7) 核心實作已透過 PR #27 合併至 `main`（`internal/safety`）：單一安全決策入口 `Gate.Decide`、`Risk`／`ApprovalPrompt`／`Approver`（依 spec.md §5.2／§6.2 FROZEN 定義）、readonly 模式的確定性拒絕（不呼叫 Approver）、無 TTY 時 `E_APPROVAL_REQUIRED_NO_TTY` 快速失敗、`run_powershell` 針對 §6.2 六類代表命令（強制／遞迴刪除、清空內容、大量移動覆寫、git 狀態變更、安裝或更新套件、網路傳輸、背景程序／job、workspace 外絕對路徑）的字串／token 分類。#4（PR #33）已在 8 工具的實際 I/O 前接上 `Gate.Decide`、`filetools`、`workspace` 與 `exec`（INV-1 反例測試已涵蓋 8 工具）；實際 TUI／純文字 Approver 仍屬 #2。best-effort classifier 的漏判／過度確認強化見 [#31](https://github.com/bext1998/brunel/issues/31)。
- [#8 F-7：實作 Provider Adapter（Pi delegated，ADR-002）](https://github.com/bext1998/brunel/issues/8) 核心實作已透過 PR #28 合併至 `main`（`internal/pirpc` 新套件＋共用的 `internal/redact` leaf 套件）：`BuildArgs` 依 spec §5.1 字面凍結命令組出啟動參數；`LaunchOptions.EffectiveProvider()` 是唯一的 effective-provider 解析點（顯式 `Provider`，否則取 `Model` 第一段前綴），`InjectCredentialsForLaunch` 用它注入憑證，因此只給 provider-prefixed model（不帶 `--provider`）也拿得到 Credential Manager key；`InjectCredentials` 要求傳入 `Credential{Provider, APIKey}`，provider 身份不符回 `E_PI_CREDENTIAL_MISMATCH` 且不注入，既有變數以 `EqualFold` 比對（Windows 大小寫不敏感）並改寫為正式名稱。`TranslateProviderError` 先以**原始**訊息分類，再只把遮罩後訊息放進公開 error（協定錯誤碼沿用 spec EC-11 的 `E_PROVIDER_PROTOCOL`）；`internal/redact.Secrets` 除啟發式規則外可接受呼叫端已知的實際 credential 值做精確替換（涵蓋 `sk-` 以外格式）。**殘留限制**：Pi 自行探索、Brunel 從未持有的 provider key 只剩啟發式遮罩，公開錯誤契約在該邊界的責任歸屬仍待 spec 決議。INV-9 的完整 AST 檢查＋CI 階段與正式 `TC-PIRPC-001` 已由 PR #36（Issue #30，CLOSED）完成（`go/ast` 解析、`+` 串接／同檔 const 解析、`json.Unmarshal` 後檢查 `type=="bash"`；`go test` 前的 CI step）。Pi 子行程啟動／生命週期管理與 RPC event 轉譯已由 PR #40（#9 核心）合併。
- [#22 F-13：建立 Alpha 1 三類 E2E fixtures](https://github.com/bext1998/brunel/issues/22) 已新增；#2、#7、#9、#14 已分別同步薄型 TUI、事故防護、EventSink 與客觀 CompletionReport 範圍。

## 阻塞 Issues

- 無規格決策阻塞 Alpha 1 實作。`docs/spec.md` §5／§9 的 Route B 修訂已於 v1.3（`7e9e01e`）完成。
- #13（完成證據狀態機）與 #15（Smoke Benchmark Runner）已依 v1.2 以 `not planned` 關閉。
- 可執行前線（無開放阻塞）：**#31**（§6.2 classifier 強化，無硬阻塞、建議發布前完成）。#4／#5／#7／#8／#9／#29 核心與 #30 皆已落地；#2 待 #9（現已解除）；#11 待 #9（現已解除）；#14 待 #9（現已解除，另需裁決 `workspace_diff` 語意 vs `Diff` 欄位——見 [#9 review 交接註解](https://github.com/bext1998/brunel/issues/9#issuecomment-5620455153)）；#22 待多項；#4 F-3 收尾項見 review 註解。

## 等待 Review

- 無。

## 等待 Merge

- 無。

## 已合併待關閉

- #4（F-3 8 工具 registry，`internal/tools`）核心經 PR #33 合併至 `main`；Pi bridge 屬 #9、真實 AC-6／AC-9 E2E 待 #9，F-3 收尾 minor 見 review 註解，Issue 關閉待定。
- #5（F-4 stale-read，`internal/filetools`）核心經 PR #26 合併至 `main`；已由 #4（PR #33）接上工具呼叫路徑、經 PR #37 可從 `--taylor-tool` 子行程呼叫，真實 Pi E2E 待 #9，Issue 關閉待定。
- #7（F-6 AUTO／CONFIRM，`internal/safety`）核心經 PR #27 合併至 `main`；已由 #4（PR #33）在 8 工具 I/O 前接上 `Gate.Decide`，TUI／純文字 Approver 屬 #2，classifier 強化屬 #31，Issue 關閉待定。
- #8（F-7 Provider Adapter，`internal/pirpc`＋`internal/redact`）核心經 PR #28 合併至 `main`；Pi 子行程啟動／RPC event 轉譯屬 #9，Issue 關閉待定。
- #29（`taylor-tools.ts` extension 與 `brunel --taylor-tool` 派工）核心經 PR #37 合併至 `main`（`cmd/brunel` 最小進入點、stdin JSON → `tools.Registry.Call` → 單一 stdout JSON、nil Approver → CONFIRM `run_powershell` 回 `E_APPROVAL_REQUIRED_NO_TTY`；`taylor-tools.ts` 註冊 8 工具純 transport；Opus 審查後修正 `BRUNEL_MODE` fail-safe／catch 區塊／`maxBuffer`）；真實 `pi --mode rpc` 端到端驗收已由 PR #40 補上子行程機制，但仍只跑過 fakepi，Issue 關閉待定。
- #9（F-8 Pi RPC 橋接，`internal/agent`＋`internal/completion`＋`internal/provider`＋`internal/pirpc` 新增檔案）核心經 [PR #40](https://github.com/bext1998/taylor-core/pull/40) 合併至 `main`：`internal/agent.Runtime` 啟動並管理 `pi --mode rpc` 子行程（Windows Job Object kill-on-close），把 RPC event 轉譯為 FROZEN `agent.Event`（顯示用）與 `session.Event`（持久化用）兩條路徑；`completion.Report` 最小填值（診斷用 `Diff`／`ToolFailures`／`RemainingRisks` 留給 #14）；AGENTS.md 注入僅工作區根目錄最小版本（就近載入屬 #11）。經 codex 隔離審查三輪修正：(1) `toolcall_end` 巢狀欄位解碼、缺少終態時的完成判定、session 寫入失敗未反映為 failed；(2) 進一步收斂為以 `message_end.stopReason` 作為完成分類的唯一依據（`agent_settled` 本身只代表「不會自動繼續」，不是成功訊號）、`applyStorageInvariant` 讓任何 exit path 只要有事件寫入失敗就強制回報 failed；(3) Windows `pi.Process`／`pi.Thread` handle 洩漏、`Close()` 非 idempotent 的競態。合併前我方重新獨立驗證：`go build`／`vet`／`test`（Windows）、`GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build`、`TestTCPIRPC001` 全數通過。**真實 `pi --mode rpc` 端到端仍未驗證**，目前只有 `internal/pirpc/testdata/fakepi` 的子行程整合測試與單元測試覆蓋。Issue 關閉待定。

## 最近完成

- PR #40（#9 F-8 核心）已合併至 `main`（squash，commit `c620351`）：見上方 #9 條目。
- PR #36（#30 F-x INV-9，`Closes #30`）已合併至 `main`：`internal/pirpc` 的 `bash` RPC command 禁令從過渡子字串 tripwire 升級為 `go/ast` 靜態檢查（解析字串字面／`+` 串接／同檔 const，`json.Unmarshal` 後查 `type=="bash"`，回報 `file:line`），`TestTCPIRPC001` 正反例，CI 於 `go test` 前執行。#30 已 CLOSED。
- PR #37（#29 核心，`Related to #29`）已合併至 `main`：新增 repo 第一個可執行檔 `cmd/brunel`（最小 `--taylor-tool` 進入點）＋ `taylor-tools.ts`／`package.json`／`tsconfig.json`。經 Opus 隔離審查（changes-requested）後修正三個 major：`BRUNEL_MODE` 改 fail-safe（未設預設 `readonly`）、`catch` 區塊不再把非 JSON 失敗吞成 `SyntaxError`、`maxBuffer` 4→64 MiB。三個隔離審查（Haiku／Sonnet／Opus）另見各 PR 討論。
- PR #38（`AGENTS.md` 工作原則第 7 條）已合併至 `main`：Git worktree 集中放 `D:\AgentCoding\.codex\worktrees\Brunel`，分支名 `maze/YYYY-MM-DD-<隨機hash>` 無字尾。
- PR #33（#4 F-3，`internal/tools`）已合併至 `main`：8 工具固定 registry、嚴格參數驗證、結構化結果、INV-1 前置裁決接線；經兩個隔離 Sonnet subagent 審查後修正兩個 major（INV-1 反例補齊 8 工具、`ErrorCode` 串接依賴碼）。殘留 minor 記於 #4 review 註解，並交叉引用 #29／#9。
- PR #34（CI ubuntu 列標籤）已合併至 `main`：matrix 改 `include` 形式加 `role`（`target`／`portability-guard`），標頭註解說明 ubuntu 列僅為可攜性／分層防線、非支援目標。check 名稱改為 `build-test (<os> - <role>)`。
- PR #32（`CLAUDE.md` 匯入 `AGENTS.md`）已合併至 `main`：讓 Claude Code 與其他 agent 共用同一份入庫指令。
- PR #28（#8 F-7 Provider Adapter，`internal/pirpc`＋`internal/redact`）已合併至 `main`；經兩輪 review 修正（provider 錯誤訊息遮罩、協定錯誤碼對齊 spec EC-11 `E_PROVIDER_PROTOCOL`、`Credential` provider 綁定與 `E_PI_CREDENTIAL_MISMATCH`、`EffectiveProvider` 前綴解析、先分類再遮罩、`redact` 已知值精確遮罩、Windows 環境變數大小寫不敏感取代、INV-9 tripwire 誠實化）。INV-9 完整 AST/CI 防線仍屬 #30。
- PR #26（#5 F-4 stale-read，`internal/filetools`）與 PR #27（#7 F-6 AUTO／CONFIRM，`internal/safety`）已合併至 `main`；均經多輪 review 修正（#26：非 Windows no-overwrite install、有界非阻塞鎖、誠實化 INV-6；#27：分隔符旁路、路徑正規化、覆寫／大量移動命令形式）。CI 於 `windows-latest` + `ubuntu-latest` 矩陣通過。
- `docs/spec.md` 提升為 **v1.3.1**：§10 INV-6 標註為 best-effort、新增 §16 OQ-10（檔案寫入的 sub-millisecond rename 競態，Alpha 1 接受為 best-effort，Alpha 3「單一 writer」時重評）。
- ADR-002 架構轉向拆解為 Issues（`maze-spec-to-issues`）：#8／#9 改標題與範圍、新增 #29／#30（皆由 #9 拆出、掛在 #1 下、assignee `bext1998`）；Gate 0 補測依使用者裁決不另建、CAND-1（OQ-8）暫緩；無新標籤。
- [ADR-002](docs/adr/ADR-002-pi-agent-runtime.md) 確認：完成 Issue #24 Pi Compatibility Spike（5 Gate，證據存於已 push 的 `agent/pi-spike-issue-24` 分支）並據此裁決放棄零依賴需求、轉向 Route B。
- PR #23（v1.2 規格與相關文件對齊）已合併至 `main`。
- 完成 v1.2 GitHub Issue 同步：更新 #1、#2、#4、#5、#7、#8、#9、#11、#14，關閉 #13／#15，新增 #22；候選 F15～F17 未建立。
- 完成並取得使用者裁決的 Alpha 1 v1.2 規格：安全收斂為事故防護、加入薄型 TUI、簡化完成報告並將 benchmark runner 移回 Alpha 4。
- #6（F-5 PowerShell Job Object 執行器）已透過 PR #20 合併至 `main` 並關閉；經三輪 review 修正 pipe read handle 重複關閉、`TerminateJobObject` 錯誤處理與有界等待（含 pipe drain）、handle 繼承 mutex 範圍不足。
- #3（F-2 Workspace）已透過 PR #19 合併至 `main` 並關閉（root 真實路徑／identity 綁定、junction／絕對路徑／symlink 逃逸攔截與 TC-WS 測試全數通過）。
- #10／#12 已分別透過 PR #16／#17 合併至 `main` 並關閉。
- 完成 Alpha 1 v1.1 規格補強；已由 v1.2 取代。
- 建立 Git、GitHub 與 Maze 專案治理基礎。
- 依 `docs/spec.md` 需求追蹤矩陣建立 #1～#15、結構化標籤與原生父子關係。

## 未追蹤本機工作

- PR #21（GitHub Actions CI workflow，`.github/workflows/ci.yml`）已合併至 `main`，無對應 Issue；每次 push／PR 自動跑 `go build`／`go vet`／`go test`／零 CGO build，矩陣 `windows-latest`（target）＋`ubuntu-latest`（portability-guard，PR #34 標籤化）。
- PR #32（`CLAUDE.md` → `@AGENTS.md`）、PR #34（CI 矩陣標籤與註解）、PR #35／PR #38（`STATUS.md`／`NEXT_ACTION.md` 同步、`AGENTS.md` 規則 7）已合併至 `main`，皆無對應 Issue（專案治理輔助）。

## 已知驗證限制

- v1.2 AC-7 的 stale-read 防護（#5）核心邏輯與單元測試已合併（PR #26），並經 #4（PR #33）接上實際 `workspace.Workspace` 與 8 工具呼叫路徑、Windows AC-6 閉環測試通過，`brunel --taylor-tool` 子行程進入點亦已合併（PR #37），Pi RPC 子行程橋接機制本身也已由 PR #40（#9 核心）合併；但真實 `pi --mode rpc` → `--taylor-tool` 這條完整路徑仍只跑過 fakepi，沒有對接真正的 Pi 執行過（AC-7／AC-6 正式判定需待真實 E2E）。
- INV-6 為 best-effort（spec v1.3.1／OQ-10）：`internal/filetools` 無法完全消除鎖內重驗與原子換檔之間的 sub-millisecond rename 競態；Alpha 1 接受（併發寫入者僅外部人為編輯，Brunel 內部無併發 writer），Alpha 3「單一 writer」時重評。
- AC-9～AC-11（AUTO 體驗、CONFIRM 分類、readonly／無 TTY）對應的 #7 核心決策邏輯與單元測試已合併（PR #27），#4（PR #33）已完成 `internal/tools` 層的工具接線與 registry 反例測試；正式判定仍待 #2（TUI／純文字 Approver 實作）與真實 Pi 呼叫路徑（#9 核心橋接機制已合併，仍待真實 E2E）後的整合測試。`--taylor-tool` 子行程無 TTY，CONFIRM `run_powershell` 固定回 `E_APPROVAL_REQUIRED_NO_TTY`（PR #37 已註明，批准 UX 實作屬 #2）。§6.2 classifier 的漏判／過度確認強化見 #31。
- AC-4（Provider 與憑證）對應的 #8 已完成 `internal/pirpc` 的 provider／model 透傳、憑證環境變數注入與錯誤轉譯核心邏輯與單元測試（PR #28），子行程啟動／管理機制已由 PR #40（#9 核心）合併，但仍未經真實 Pi RPC 子行程驗證（僅 fakepi）；`OPENROUTER_API_KEY` 等憑證環境變數命名依 Issue #24 Spike 分支的偵測結果，未對照 Pi 正式文件逐一確認。Pi 自行探索、Brunel 從未持有的 provider key 若被回顯於錯誤訊息，只能做啟發式遮罩；公開錯誤不含 secret 的最終責任邊界待 spec 定義。
- PR #41（原 STATUS/NEXT_ACTION 同步文件，內容已過期）由使用者主動 CLOSED（未合併），本輪同步取代其內容。
- `internal/exec` 的 Timeout／MaxProcesses／MaxMemoryBytes／MaxOutputBytes 一律由呼叫端明確提供，套件本身不內建預設值；Alpha 1 不再需要 benchmark 硬性預算。
- repository 的 `go.mod` 目前仍是 Go 1.22；本次依約不修改程式碼或依賴，後續實作 TUI 前需另行同步至 Go 1.25.x。
