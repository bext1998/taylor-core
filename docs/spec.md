# Brunel Alpha 1 Specification

**版本**：v1.3.2

**狀態**：Approved

**日期**：2026-09-18

**適用對象**：實作工程師、AI 代理（Claude Code / Codex）、規格審查者

**技術環境**：Go 1.25.x（Host：TUI、Workspace、Safety、PowerShell／Job Object、Session、CompletionReport）、Pi（model-facing Agent Runtime，經 `pi --mode rpc` 呼叫，需 Node.js/npm 執行環境）、零 CGO、靜態編譯、Windows x64、PowerShell 7（`pwsh`）
**前置文件**：`docs/Brunel_產品提案.md`、`docs/adr/ADR-001-runtime-language.md`、`docs/adr/ADR-002-pi-agent-runtime.md`

---

## 1. 文件目的與產品命題

Brunel 是面向進階個人開發者的薄型 coding harness。它要驗證：當模型已有足夠的自主推理與工具使用能力時，Harness 是否能只提供工具、事故防護、上下文透明度與完成事實，而不以固定工作流、強制規劃或龐大提示替模型做決定。

Alpha 1 的成功不是「提供完整安全沙箱」，而是讓使用者在受控 workspace 中，以單一 Windows 執行檔完成理解、修改與驗證閉環，並清楚看見模型做了什麼。

`[FROZEN]` 標記的介面與不變式不得由實作者自行修改。變更必須先修訂規格並取得使用者裁決。

### 1.1 已確認決策

- Go 基線為 1.25.x；`GOOS=windows GOARCH=amd64 CGO_ENABLED=0`。
- 互動模式採 Bubble Tea v2 的薄型全螢幕 TUI。
- 安全定位是事故防護，不是 sandbox，也不對抗惡意 repository。
- 批准只有 `AUTO` 與 `CONFIRM`；需確認的操作每次都問，不保存授權。
- Alpha 1 不實作 benchmark runner；完整 runner 與硬性預算留到 Alpha 4。
- CompletionReport 只記錄客觀事實，不驗證模型自訂條件的語意正確性。
- **（ADR-002）** Model-facing Agent Loop 與 Provider abstraction 交給 Pi（`pi --mode rpc`）負責；Go Host 保留 Workspace、Safety、PowerShell／Job Object、Session、CompletionReport 這幾項 Host-only authority，8 個固定工具全部維持 Go 實作，經 Taylor custom tools 暴露給 Pi。Provider 選擇不再限定 OpenRouter，交由 Pi 支援的 provider 生態決定；Brunel 自己的 session 維持為對外正式紀錄，Pi 自身的 session 持久化停用（`--no-session`）。

### 1.2 與 Taylor / Watt 的關係 [FROZEN]

Brunel 與 Taylor、Watt 工程上完全獨立，不 import、偵測、呼叫或整合兩者。CompletionReport 是 Brunel 自有格式；未來其他產品只能單向讀取。

---

## 2. 目標與非目標

### 2.1 Goals

- **G-1**：乾淨 Windows x64 環境下載 Brunel 後即可執行；需另外安裝 Node.js/npm 作為 Pi（model-facing Agent Runtime）的執行環境，比照現有 PowerShell 7 依賴的揭露方式，不再承諾零依賴單檔 exe（見 [ADR-002](adr/ADR-002-pi-agent-runtime.md)）。目前設計（Taylor RPC client 停用 Pi 全部內建工具、絕不送出 `bash` RPC command）理論上不需要另外安裝 Git Bash；此點待正式 RPC client 實作完成、且在未裝 Git Bash 的乾淨環境驗證後才能確認為既定事實（Issue #24 Gate 0 遺留待辦）。
- **G-2**：模型可在固定 workspace 內使用 8 個內建工具完成理解、修改與驗證。
- **G-3**：一般開發操作不打斷使用者；明顯風險操作會逐次確認。
- **G-4**：互動模式以薄型 TUI 顯示對話、工具活動、用量、狀態與批准請求。
- **G-5**：Session 事件保留於本機 append-only log；上下文裁剪不刪除原始紀錄。
- **G-6**：完成時輸出可檢查的 diff、驗證命令、工具失敗、剩餘風險與成本事實。

### 2.2 Non-Goals

- 不提供 sandbox、惡意 repository 隔離或完整 PowerShell 語意安全分析。
- 不實作 MCP、通用 plugin system、Taylor connector、LSP、瀏覽器或專用 web 工具。
- 不實作技能系統、subagent、完整 Git 抽象、worktree、PR 或 GitHub 整合。
- 不提供自動 commit、push、發布或部署功能；透過 PowerShell 執行時仍依安全分類逐次確認。
- 不支援 macOS、Linux、Windows PowerShell 5.1、CMD、Git Bash 或 WSL。
- 不實作驗證 pipeline、專案類型知識、驗證快取或歷史統計。
- 不加密 session；只有憑證存入 Windows Credential Manager。
- TUI 不提供檔案樹、diff viewer、session browser、設定頁或滑鼠工作流。
- Alpha 1 不實作 smoke benchmark framework、比較組、硬性 token／費用預算或聚合報表。

---

## 3. 使用情境

### 3.1 互動式工作

使用者在專案目錄執行 `brunel`。程式進入全螢幕 TUI，使用者輸入任務，模型串流回覆並執行 workspace 內的一般開發操作。TUI 顯示 transcript、工具狀態、模型與 token 用量；需確認的命令以 modal 逐次詢問。完成後顯示成果、驗證與剩餘風險。

### 3.2 單次任務

`brunel "<task>" --report out.json` 使用純文字串流，不進入 alternate screen。無 TTY 且遇到需確認操作時，以 `E_APPROVAL_REQUIRED_NO_TTY` 及非零退出碼結束，不得阻塞。

### 3.3 唯讀分析

`brunel --mode readonly "<task>"` 允許 `list_files`、`search_text`、`read_file`、`workspace_diff`；所有寫入工具與 `run_powershell` 在執行前直接拒絕，不詢問。

### 3.4 Session 恢復

`brunel --resume <name|id>` 恢復摘要、目標、決策、diff、驗證結果與未完成事項；不恢復舊程序或任何批准狀態。

---

## 4. Alpha 1 功能與追蹤矩陣

| Task ID | 功能 | 目標與範圍 | 非範圍 | 依賴 | 主要風險 | 優先級／狀態 | 驗收 |
|---|---|---|---|---|---|---|---|
| TASK-A1-F01 | CLI 與 TUI | 互動 TUI、單次模式、flags、TTY 判斷 | TUI 擴充面板 | config、agent | UI 與核心耦合 | P1／正式 | AC-1～AC-3 |
| TASK-A1-F02 | Workspace | root identity、真實路徑與逃逸攔截 | OS sandbox | Windows API | junction 逃逸 | P1／正式 | AC-7、AC-8 |
| TASK-A1-F03 | 8 個工具 | 固定 schema、參數驗證與結果 | plugin、額外工具 | workspace、exec | 工具契約漂移 | P1／正式 `[FROZEN]` | AC-6 |
| TASK-A1-F04 | Stale read | hash 前置條件與原子寫入 | 自動 merge | workspace | 覆蓋外部修改 | P1／正式 | AC-7 |
| TASK-A1-F05 | PowerShell | pwsh 7、Job Object、逾時與取消 | 命令 sandbox | Windows API | 子程序逃逸 | P1／正式 | AC-12 |
| TASK-A1-F06 | 事故防護 | AUTO／CONFIRM、readonly、無 TTY 行為 | 完整語意分類 | tools、TUI | 漏判、誤判 | P1／正式 `[FROZEN]` | AC-9～AC-11 |
| TASK-A1-F07 | Provider（Pi delegated，ADR-002） | 透傳 `--provider`／`--model` 給 Pi；不再限定 OpenRouter，provider 覆蓋範圍由 Pi 生態決定；轉譯 Pi 回報的錯誤與 usage | 自行實作 SSE、probe、retry（改由 Pi 負責） | Pi RPC、Credential Manager | Pi 版本行為變化、provider 覆蓋範圍不透明 | P1／正式 | AC-4 |
| TASK-A1-F08 | Agent（Pi RPC 橋接，ADR-002） | 啟動與管理 Pi RPC 子行程；把 Pi RPC event 轉譯為 Brunel `EventSink`；注入 AGENTS.md 與 8 個 Taylor tools；Taylor RPC client 絕不送出 `bash` RPC command | 自行實作 agent loop、context 裁剪與摘要（改由 Pi 負責，Brunel 只 append 已轉譯 event） | Pi RPC、tools、session | RPC 邊界洩漏、`bash` side channel 被誤用 | P1／正式 | AC-2、AC-14 |
| TASK-A1-F09 | Session | 保存、命名、恢復、清理與遮罩 | 加密、跨裝置同步 | filesystem | 敏感內容、損毀 | P1／正式 | AC-13 |
| TASK-A1-F10 | AGENTS.md | root 與就近規則載入 | 權限授予 | context、workspace | prompt injection | P1／正式 | AC-5 |
| TASK-A1-F11 | Config | CLI／project／global／default 與憑證分離 | 專案憑證 | Credential Manager | 設定漂移 | P1／正式 | AC-4 |
| TASK-A1-F12 | CompletionReport | 客觀完成事實與 JSON 輸出 | 語意驗收引擎 | diff、events | 錯誤完成聲明 | P1／正式 `[FROZEN]` | AC-15 |
| TASK-A1-F13 | E2E fixtures | 三類固定真實任務 | benchmark runner | 完整 agent | fixture 漂移 | P1／正式 | AC-16 |
| TASK-A1-F15 | Context ledger | 顯示 context token 來源分布 | 歷史統計 | context | usage 粒度不足 | 候選／不擋發布 | 候選 AC |
| TASK-A1-F16 | Prompt 透明化 | 顯示實際 prompt 與 token | 修改安全核心 | context | secret 洩漏 | 候選／不擋發布 | 候選 AC |
| TASK-A1-F17 | 非 Git diff | 以 session 快照產生 diff | 完整 VCS | workspace、session | 大 workspace 成本 | 候選／不擋發布 | 候選 AC |

候選功能不得因列入矩陣而升級為 Alpha 1 承諾。

### 4.1 CLI 契約

- `brunel`：TTY 中啟動互動 TUI。
- `brunel "<task>"`：單次純文字模式；task 去除前後空白後不得為空。
- `--mode workspace|readonly`，預設 `workspace`。
- `--name <name>`、`--resume <name|id>`、`--report <path>`、`--model <id>`。
- `--report` 不使程式進入 TUI；report 仍於任務終態寫出。

### 4.2 TUI 契約 [FROZEN]

Bubble Tea 只存在於 presentation 層，不得被 `agent`、`pirpc`、`tools` 或 `session` import。

TUI 只包含：

1. 可捲動 transcript。
2. 多行輸入區。
3. 模型、token、成本與目前執行狀態列。
4. 顯示命令與原因的批准／拒絕 modal。

視窗 resize 不得中止任務；終端過窄時優先保留 transcript、輸入與批准資訊。執行中 Ctrl+C 傳遞取消；閒置時 Ctrl+C 退出。取消後不得留下執行中的工具程序。

---

## 5. 架構與公開介面

### 5.1 模組邊界（ADR-002 修訂）

```text
cmd/brunel                              Go host process（單一執行檔）
  ├── internal/tui                      Bubble Tea presentation
  ├── internal/config
  ├── internal/session                  Brunel 自己的 session／events.jsonl；Pi 自身 session 停用
  └── internal/agent
        ├── internal/pirpc              管理 Pi RPC 子行程、event 轉譯、`bash` command 禁止清單
        ├── internal/completion
        └── internal/tools              8 個 Taylor tools 的 Go 實作
              ├── internal/safety
              ├── internal/workspace
              └── internal/exec

taylor-tools.ts（Pi extension，Node）   以 `pi.registerTool()` 註冊 8 個 Taylor tools；每次呼叫轉為
                                         子行程呼叫 `brunel.exe --taylor-tool <name>`，重用同一支
                                         Go binary 執行實際 I/O（沿用 Issue #24 Gate 3/4 驗證過的模式）
```

`cmd` 選擇 TUI 或純文字 sink；`internal/pirpc` 啟動 `pi --mode rpc --no-builtin-tools --no-extensions -e taylor-tools.ts --no-session --provider <p> --model <m>` 子行程，將其 RPC event 轉譯為 `agent.Event`。Model-facing agent loop 邏輯本身不在 Go 內實作，只在 Pi 內；Go 不知道實際 presentation，也不參與模型推理決策。禁止循環依賴。

### 5.2 Agent 與事件介面 [FROZEN]

```go
package agent

type Agent interface {
    Run(ctx context.Context, task string, sink EventSink) (*completion.Report, error)
}

type EventSink interface {
    Emit(Event)
}

type Event struct {
    Kind       EventKind
    Timestamp  time.Time
    Text       string
    ToolCallID string
    ToolName   string
    Usage      provider.Usage
}

const (
    EventAssistantDelta  EventKind = "assistant_delta"
    EventToolStarted     EventKind = "tool_started"
    EventToolFinished    EventKind = "tool_finished"
    EventApprovalNeeded  EventKind = "approval_needed"
    EventApprovalResolved EventKind = "approval_resolved"
    EventUsageUpdated    EventKind = "usage_updated"
    EventRunFinished     EventKind = "run_finished"
)
```

`EventSink` 只傳遞顯示事件，不授權工具、不改變 agent 決策。TUI 與純文字輸出必須消費相同事件來源。

**ADR-002 註記**：`Agent` 介面本身不變（TUI／純文字 sink 消費同一組 `Event`）。內部實作改為由 `internal/pirpc` 啟動並管理 Pi RPC 子行程，把 Pi 的 RPC event（`message_update`、`tool_execution_start`／`end`、`agent_end` 等）轉譯為上述 `EventKind`；`internal/pirpc` 是唯一允許與 Pi RPC 對話的模組，且明確禁止送出 `{"type":"bash"}` command（見 §9 CT-6、Issue #24 Gate 2）。

批准回應使用獨立的 UI-neutral port：

```go
package safety

type ApprovalPrompt struct {
    Command string
    Reason  string
}

type Approver interface {
    Confirm(ctx context.Context, prompt ApprovalPrompt) (bool, error)
}
```

TUI 與有 TTY 的純文字模式各自實作 `Approver`；無 TTY 時不提供 approver。安全決策入口負責呼叫 `Approver`，`EventSink` 只能同步顯示批准請求與結果。

### 5.3 Provider（ADR-002：委派給 Pi，不再 FROZEN）

Provider 選擇、SSE streaming 解析、tool-call probe 與 retry 不再由 Brunel 自行實作，改由 Pi 負責。Brunel 的責任範圍縮小為：

- 將使用者指定的 `--model`（含 provider 前綴，語法依 Pi 慣例）透傳給 `pi --mode rpc` 啟動參數；不再限定只支援 OpenRouter，實際可用 provider 範圍等於當前 Pi 版本支援的範圍，須在 README／CLI help 明確揭露為「隨 Pi 版本變動」，不得由 Brunel 片面承諾涵蓋範圍。
- 憑證仍優先存 Windows Credential Manager；`internal/pirpc` 啟動 Pi 子行程時透過環境變數或 Pi 既有的 credential 機制傳入，不得寫入專案設定檔。
- Pi 回報的 provider 層錯誤（認證、額度、模型不存在、協定錯誤）由 `internal/pirpc` 轉譯為 Brunel 錯誤碼並顯示；Brunel 不重新實作重試邏輯，也不得覆蓋或攔截 Pi 已決定的重試／放棄行為。

不得以純文字模擬工具呼叫；此限制的落實責任已隨 Provider 邏輯一併轉移給 Pi。

### 5.4 工具 Schema [FROZEN]

Alpha 1 固定以下 8 個工具，不得新增、刪除或改名：

| 名稱 | 主要參數 | 回傳 |
|---|---|---|
| `list_files` | `path`, `glob?`, `max_depth?` | 路徑與大小 |
| `search_text` | `pattern`, `path?`, `glob?`, `max_results?` | 檔名、行號與命中內容 |
| `read_file` | `path`, `start_line?`, `end_line?` | 帶行號內容與全檔 SHA-256 |
| `apply_patch` | `path`, `expected_hash`, `hunks[]` | 新 SHA-256 |
| `create_file` | `path`, `content` | 新 SHA-256；已存在則失敗 |
| `write_file` | `path`, `expected_hash`, `content` | 新 SHA-256 |
| `run_powershell` | `command`, `timeout_sec?`, `cwd?` | stdout、stderr、exit code、truncated |
| `workspace_diff` | `path?` | unified diff |

所有 path 先經 workspace 真實路徑解析。`apply_patch` 與 `write_file` 必須帶最近一次完整讀取得到的 `expected_hash`。檔案工具優先於以 PowerShell 寫檔。

**ADR-002 註記**：8 個工具全部維持 Go 實作，透過 `taylor-tools.ts`（Pi extension）以 `pi.registerTool()` 註冊給 Pi 內的模型，不使用 Pi 任何內建工具（`--no-builtin-tools --no-extensions`）。模型呼叫工具時，extension 轉為子行程呼叫 Go 實作；實際 I/O、路徑解析與安全裁決全部發生在 Go 內，Node 層只做參數轉送與結果回傳，不做任何業務邏輯判斷。

---

## 6. 安全與事故防護 [FROZEN]

### 6.1 能力聲明

> **Brunel 提供事故防護，不提供 sandbox。**
> PowerShell 是完整程式語言；Brunel 只能辨識少量明顯命令形式，無法可靠辨識別名、腳本、編碼內容或間接呼叫。請勿在惡意或不受信任的 repository 中執行 workspace 模式。

**ADR-002 註記**：Pi 不是安全決策入口，也不繞過它。無論工具呼叫來自 Pi 內的模型或任何未來呼叫端，8 個工具的 I/O 前置條件與風險裁決都在 Go（`internal/safety`）執行，與 §5.1 的模組邊界一致；INV-1（見 §10）不因呼叫端改變而放寬。

### 6.2 決策模型

```go
type Risk int

const (
    RiskAuto Risk = iota
    RiskConfirm
)
```

- **AUTO**：workspace 內的一般讀取、搜尋、精確寫入、測試、lint、typecheck、build 與唯讀 Git 操作。
- **CONFIRM**：每次都顯示完整命令與分類原因，使用者只可批准本次或拒絕；不得記憶批准。

以下明顯命令形式必須確認：

- 遞迴或強制刪除、清空內容、大量移動或覆寫。
- `git commit`、`git push`、`git reset --hard`、`git clean`、`git rebase`。
- 安裝或更新依賴與系統套件。
- 明確網路傳輸命令，例如 `Invoke-WebRequest`、`Invoke-RestMethod`、`curl`、`wget`。
- 背景程序、PowerShell job 或 `Start-Process`。
- 命令文字中明確出現 workspace 外的絕對路徑並可能讀寫該路徑。

分類是最佳努力的字串與 token 判斷，不得宣稱涵蓋完整 PowerShell。測試只驗證列出的代表性命令與不產生副作用，不驗證語言完備性。

### 6.3 確定性拒絕

以下不是風險分類，而是工具前置條件；不符合即直接拒絕且不得產生副作用：

- readonly 模式中的任何寫入工具與 `run_powershell`。
- 檔案工具解析後路徑逃逸 workspace。
- stale hash、patch conflict 或 `create_file` 目標已存在。
- workspace 未綁定或 identity 已失效。

無 TTY 且命令需要確認時，立即回 `E_APPROVAL_REQUIRED_NO_TTY` 並以非零狀態結束本次 run。

### 6.4 程序控制

所有 PowerShell 子程序必須先綁入 Windows Job Object 才開始執行。支援逾時、取消、整棵程序樹終止、程序數與記憶體上限，以及 `JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE`。一般模式的資源值由呼叫端設定；Alpha 1 不定義 benchmark 預算。

---

## 7. Session、Context、AGENTS.md 與設定

### 7.1 Session

- Session 使用不可變 ULID；名稱可重複。
- 未命名 session 正常退出後刪除；異常中止預設保留 72 小時。
- `events.jsonl` 只 append；summary、resume 與清理不得改寫既有完整 event。
- 恢復時載入摘要、目標、決策、diff、驗證與未完成事項，不重建 shell 程序。
- 落盤前盡力遮罩已知 secret 模式，但不宣稱完整偵測；session 不加密。
- 同名 session 無法唯一解析時回 `E_SESSION_AMBIGUOUS` 並列出 ID 與時間。
- **（ADR-002）** Pi 子行程一律以 `--no-session` 啟動，停用其自身的 session 持久化；Brunel 自己的 `events.jsonl` 是唯一對外正式紀錄，由 `internal/pirpc` 轉譯 Pi RPC event 後 append 寫入。

### 7.2 Context

Context 必須保留使用者指令、目前目標、使用者決策、適用的 AGENTS.md、已修改檔案、最新 diff、驗證結果、未解錯誤與未完成事項。冗長或過時工具輸出可摘要或裁剪，但原始 event 仍留在 append-only log。

### 7.3 AGENTS.md

啟動時注入 workspace root 的 AGENTS.md；子目錄規則於首次觸及該目錄的成功工具結果送達，自該次之後對同目錄的後續操作生效，首次操作不受此層規則約束。AGENTS.md 只能約束工作方式，不能批准工具、改變安全分類或繞過工具前置條件。

### 7.4 設定與憑證

優先序為 CLI flags > `<workspace>\.brunel\config.json` > `%USERPROFILE%\.brunel\config.json` > 預設。API key 只存 Windows Credential Manager；專案設定不得保存憑證。

---

## 8. CompletionReport Schema [FROZEN]

目前尚無已發布 schema 或外部讀取端，因此 v1.2 直接定義未發布的 schema `1.0`，不建立遷移層。

```go
type Report struct {
    SchemaVersion   string         `json:"schema_version"` // "1.0"
    SessionID       string         `json:"session_id"`
    Task            string         `json:"task"`
    Status          string         `json:"status"` // completed | incomplete | failed
    ModifiedFiles   []string       `json:"modified_files"`
    Diff            string         `json:"diff"`
    Verifications   []Verification `json:"verifications"`
    ToolFailures    []ToolFailure  `json:"tool_failures"`
    PendingApproval *ApprovalFact  `json:"pending_approval,omitempty"`
    RemainingRisks  []string       `json:"remaining_risks"`
    Cost            CostSummary    `json:"cost"`
}

type Verification struct {
    Command  string `json:"command"`
    ExitCode int    `json:"exit_code"`
    Summary  string `json:"summary"`
}

type ApprovalFact struct {
    Command string `json:"command"`
    Reason  string `json:"reason"`
}

type CostSummary struct {
    PromptTokens     int      `json:"prompt_tokens"`
    CompletionTokens int      `json:"completion_tokens"`
    CostUSD          *float64 `json:"cost_usd"`
    DurationSec      float64  `json:"duration_sec"`
    Turns            int      `json:"turns"`
}
```

- `completed`：模型正常結束、所有 tool call 都有終態且沒有待批准操作。
- `incomplete`：使用者取消、拒絕批准，或模型明確表示無法繼續。
- `failed`：provider、協定、workspace 或其他不可恢復錯誤使 run 終止。
- 驗證命令非零退出碼只記錄事實，不單獨決定 status。
- Harness 不理解任務語意，也不宣稱 `completed` 等於需求正確完成。

Report 以 UTF-8 JSON 寫入 workspace 內既存父目錄，採暫存檔後原子替換。既有目標檔的策略仍由 OQ-3 裁決。

---

## 9. Contract

| ID | 輸入要求 | 成功輸出 | 失敗行為 |
|---|---|---|---|
| CT-1 CLI | 互動模式需 TTY；單次 task 不得為空 | TUI 或純文字串流 | 空 task 回 `E_INVALID_ARGUMENT`，不呼叫 provider |
| CT-2 Workspace | 啟動目錄存在、可讀且可解析真實路徑 | session 固定 root identity | 無效回 `E_WORKSPACE_INVALID`；不得 fallback |
| CT-3 Tool call | 名稱屬 8 工具且參數符合 schema | 結構化結果與終態 event | 未知或錯型參數不得自行補值 |
| CT-4 寫入 | hash 與 patch context 正確 | 原子寫入與新 hash | 任一失敗保留完整原檔 |
| CT-5 PowerShell | pwsh 7、cwd 位於 workspace、限制值明確 | 受 Job Object 控制的結果 | 需確認、取消、逾時均有穩定錯誤與終態 |
| CT-6 Provider | `--model` 語法符合 Pi 慣例；Pi RPC 子行程已就緒 | 由 `internal/pirpc` 轉譯的 text、tool calls、usage event | Pi 回報的 provider 層失敗經轉譯為穩定錯誤碼；Brunel 不自行重試或靜默切換，不覆蓋 Pi 已決定的行為 |
| CT-7 Session resume | name 可唯一解析或直接使用 ID | 恢復結構化狀態 | 無匹配或多重匹配不得自選 |
| CT-8 Report | 路徑在 workspace、父目錄存在且可寫 | 原子產生 schema 1.0 JSON | 不留下宣稱 completed 的部分檔案 |

所有公開錯誤至少包含穩定錯誤碼與可行短訊息；不得包含 API key、Authorization header 或未遮罩的已知 secret。

---

## 10. Invariants

| ID | 不變式 | 違反症狀 | 必要測試 |
|---|---|---|---|
| INV-1 `[FROZEN]` | 每個工具在 I/O 前都經同一安全決策入口 | 無決策 event 即出現副作用 | 以拒絕決策驗證 8 工具無旁路 |
| INV-2 `[FROZEN]` | events.jsonl 只 append | resume 或摘要後舊 bytes 改變 | 摘要前後比較既有 prefix |
| INV-3 `[FROZEN]` | TUI／sink 不得授權或改變 agent 決策 | presentation 事件直接執行工具 | fake sink 不得影響工具結果 |
| INV-4 `[FROZEN]` | 只有安全決策入口可呼叫 Approver | TUI 直接放行工具或 agent 繞過 gate | fake approver 與拒絕路徑 integration test |
| INV-5 | workspace root identity 在 session 內不變 | junction 替換後操作到另一位置 | identity 與逃逸回歸測試 |
| INV-6（best-effort） | 寫入不覆蓋未知新版本：`expected_hash` 前置條件 + 鎖內重驗 hash + 暫存檔原子換檔；hash 不符／patch conflict／寫入失敗一律保留原檔。無法完全消除鎖內重驗與換檔之間的 sub-millisecond rename 競態（見 OQ-10）。 | stale 或失敗後檔案 hash 改變 | stale、conflict、磁碟錯誤、鎖衝突有界失敗測試 |
| INV-7 | 取消或逾時不留下子孫程序 | run 結束後程序仍存活 | Job Object E2E |
| INV-8 | completed report 只含已有終態的 tool call | pending call 被宣稱完成 | 逐項移除終態的反例測試 |
| INV-9 `[FROZEN]`（ADR-002） | `internal/pirpc` 絕不主動送出 `{"type":"bash"}` RPC command | Pi host-level bash side channel 被 Brunel 自己的 client 使用 | 對 `internal/pirpc` 原始碼做 CI lint／AST 檢查，禁止出現該 literal；見 Issue #24 Gate 2 |

---

## 11. Edge Cases

| ID | 條件 | 預期結果 |
|---|---|---|
| EC-1 | task 為空或全空白 | `E_INVALID_ARGUMENT`；不建立可恢復 session |
| EC-2 | 工作目錄不存在、不可讀或不可解析 | `E_WORKSPACE_INVALID`；不 fallback |
| EC-3 | 中文、空白、emoji、大小寫 alias 或 Windows 保留名稱 | 依最終 Windows 路徑語意處理；不支援則穩定失敗 |
| EC-4 | resize 或極窄終端 | 任務不中止；保留 transcript、輸入與批准資訊 |
| EC-5 | Ctrl+C 發生於 provider、tool 或 report 寫入 | 傳遞取消、終止程序樹、不留下部分 report |
| EC-6 | 無 TTY 遇到需確認命令 | `E_APPROVAL_REQUIRED_NO_TTY`；不執行命令 |
| EC-7 | events.jsonl 尾端是不完整 JSON line | 保留完整 event、隔離殘片並警告 |
| EC-8 | 同名 session 多筆 | 回 `E_SESSION_AMBIGUOUS`；不自動挑選 |
| EC-9 | create／patch／cleanup／resume 重複執行 | 不覆寫、不重播副作用；cleanup 對不存在目標成功 |
| EC-10 | 磁碟滿、權限撤銷或防毒鎖檔 | 原檔保持完整；event/report 不宣稱成功 |
| EC-11 | Provider malformed SSE、重複 tool ID 或未知 finish reason | `E_PROVIDER_PROTOCOL`；不重播可能已有副作用的 call |
| EC-12 | 非 Windows 或 pwsh 7 不存在 | 工作前回 `E_UNSUPPORTED_PLATFORM` 或 `E_PWSH_REQUIRED` |
| EC-13（ADR-002） | Node.js/npm 不存在或 `pi` 無法啟動 | 工作前回 `E_PI_RUNTIME_REQUIRED`，附安裝指引連結；不得 fallback 或以其他方式模擬 Agent Loop |

---

## 12. Acceptance Criteria

| ID | 驗收項目 | 測量方式 | 通過標準 | 自動化 |
|---|---|---|---|---|
| AC-1（ADR-002 修訂） | 已知依賴齊備可執行 | 已裝 Node.js/npm、pwsh 7、Git for Windows 的乾淨 Windows 11 VM 啟動 exe | 無非預期缺失；缺少已知依賴時顯示可行動錯誤訊息（不是靜默失敗），不再承諾零依賴 | E2E + 人工 |
| AC-2 | 互動 TUI | TTY 啟動、輸入任務、resize、串流、取消 | 四個必要區域可用；無 orphan process | E2E + 人工 |
| AC-3 | 純文字模式 | pipe 執行單次 task 與 `--report` | 不進 alternate screen；輸出與 JSON 完整 | E2E |
| AC-4 | Provider 與憑證 | Credential Manager key、模型清單與 probe | 只用 tool-capable model；key 不落專案檔 | Integration |
| AC-5 | AGENTS.md | root／子目錄各放規則後操作子檔 | 首次操作後、同目錄後續操作時就近規則生效，且不得授權工具 | Integration |
| AC-6 | 8 工具閉環 | 搜尋→讀取→patch→test→diff | 全部成功且 diff 正確 | E2E |
| AC-7 | stale-write | 讀取後外部改檔再寫入 | 穩定錯誤；檔案未覆寫 | Integration |
| AC-8 | path escape | junction、symlink、絕對路徑逃逸 | 三者皆無副作用地拒絕 | Integration |
| AC-9 | AUTO 體驗 | 完成 AC-6 | 0 次批准詢問 | E2E |
| AC-10 | CONFIRM 分類 | 各類代表命令各執行一次 | 每次皆顯示命令與理由；拒絕時無副作用 | Integration |
| AC-11 | readonly 與無 TTY | 嘗試寫入／shell；pipe 執行需確認命令 | 前者直接拒絕；後者穩定退出且不執行 | Integration |
| AC-12 | 程序控制 | 啟動孫程序後逾時與取消 | 整棵程序樹終止 | Windows E2E |
| AC-13 | Session | 命名、同名、異常中止、正常退出與 resume | 保存／清理／歧義行為符合 §7.1 | Integration |
| AC-14 | Context | 觸發摘要後重載被裁剪內容 | 原 event 可定位且舊 bytes 不變 | Integration |
| AC-15 | CompletionReport | 成功、取消、工具失敗、驗證失敗、待批准 | status 與所有客觀欄位正確；JSON 原子寫入 | Integration |
| AC-16 | 三類真實任務 | bug 修復、小功能、失敗測試診斷 | 3 個 fixture 均完成工具閉環並產生 report | Windows E2E |

Alpha 1 發布門檻為 AC-1～AC-16 全部通過。候選功能不阻塞發布。

---

## 13. Test Plan

| 前綴 | 範圍 | 最低案例 |
|---|---|---|
| TC-CLI／TC-TUI | flags、TTY、事件渲染、resize、取消 | 空輸入、純文字、窄終端、modal |
| TC-WS／TC-FILE | 路徑、hash、patch、原子寫入 | junction、Unicode、stale、磁碟錯誤 |
| TC-SAFE | AUTO、CONFIRM、確定性拒絕 | 每類代表命令、拒絕無副作用、無 TTY |
| TC-EXEC | Job Object、timeout、cancel、限制 | 子孫程序全滅、先綁後執行 |
| TC-PIRPC（原 TC-PROV，ADR-002） | Pi RPC 子行程啟動、event 轉譯、`bash` 禁止清單 | Pi 啟動失敗、RPC event malformed、`internal/pirpc` 原始碼不含 `bash` command literal、子行程異常結束 |
| TC-SESSION | append、resume、cleanup、損毀 | 同名、尾端殘片、72 小時邊界 |
| TC-COMP | report schema 與終態 | success、incomplete、failed、原子寫入 |
| TC-E2E | 三類真實任務 | fixture 隔離、diff 與 report |

保護 `[FROZEN]` 契約的最低測試為：`TC-SAFE-001`（單一決策入口）、`TC-SESSION-001`（append-only）、`TC-TUI-001`（sink 不授權）、`TC-PIRPC-001`（`internal/pirpc` 不含 `bash` command literal，對應 INV-9）、`TC-COMP-001`（schema 1.0）。

禁止行為：

- 不得忽略退出碼、跳過失敗案例或只記 log 便宣稱通過。
- 不得只斷言錯誤文字；至少檢查穩定錯誤碼與無副作用。
- Unit／Integration 不得呼叫真實 OpenRouter。
- 不得使用真實使用者 session、Credential Manager 項目或來源 workspace。
- 不得以 mock 掉安全決策入口的方式宣稱 INV-1 已驗證。
- 不測試完整 PowerShell 語言分類；只測正式列出的代表命令。

驗證順序：靜態／schema → unit → integration → Windows E2E → 乾淨 VM。前一層失敗不得由後一層成功抵銷。

---

## 14. FROZEN 與變更同步

| 凍結範圍 | 變更時必須同步 |
|---|---|
| TUI、EventSink 與 Approver 邊界 | Go 介面、純文字 sink／approver、TUI 測試、CLI 文件 |
| 8 個工具名稱與 schema | Go 型別、JSON schema、prompt 描述、snapshot tests |
| 安全能力聲明與 AUTO／CONFIRM | classifier、CLI help、README、TC-SAFE |
| CompletionReport 1.0 | Go 型別、golden JSON、README 範例、相容性說明 |
| append-only session | event writer、resume reader、舊版 fixtures |
| Phase 邊界 | Non-Goals、roadmap、依賴禁止測試 |
| Taylor RPC client 的 `bash` 禁止清單（INV-9，ADR-002） | `internal/pirpc` 原始碼、CI lint／AST 檢查、`TC-PIRPC-001` |

變更程序：提出 revision → 說明相容性 → 更新同步面 → 使用者裁決 → 提升規格版本。未完成前不得合併衝突實作。

---

## 15. Drift Risk

| ID | 漂移面 | 早期訊號 | 控制 |
|---|---|---|---|
| DR-1 | TUI／核心 | agent import Bubble Tea 或純文字行為不同 | EventSink 邊界與雙 sink contract tests |
| DR-2 | Tool schema | Go 型別、prompt、文件欄位不同 | 唯一 schema 來源與 snapshot |
| DR-3 | Safety | 分散分類或重新出現批准記憶 | 單一 classifier 與 TC-SAFE-001 |
| DR-4 | Session | writer／reader 對 event kind 理解不同 | schema version、round trip、舊 fixture |
| DR-5 | Completion | report 出現不可觀察的語意聲明 | schema golden 與客觀欄位 review |
| DR-6 | Phase creep | Alpha 1 出現 benchmark、skills、subagent 邏輯 | 禁止依賴掃描與發布 checklist |
| DR-7 | Windows | 開發機通過但 Unicode／不同 volume 失敗 | 真實 Windows matrix 與乾淨 VM |

---

## 16. Open Questions

| ID | 待裁決事項 | 裁決前行為 |
|---|---|---|
| OQ-1 | Windows 最低支援版本與乾淨 VM matrix | 發布聲明不超出實測版本 |
| OQ-2 | `brunel.exe` 是否做程式碼簽章 | Alpha 版揭露 SmartScreen；不阻塞實作 |
| OQ-3 | `--report` 遇到既有檔案是否新增 overwrite flag | 回 `E_FILE_EXISTS`，不覆寫 |
| OQ-4 | events.jsonl 尾端殘片是否可自動截除 | 不改原檔；隔離殘片並警告 |
| OQ-5 | search_text 自行實作或攜帶 ripgrep | 不新增外部執行檔，待實作前裁決 |
| OQ-6 | 非 Git workspace diff 是否納入 Alpha 1 | 維持候選，不阻塞發布 |
| OQ-7 | Context ledger 與 prompt 透明化是否納入 Alpha 1 | 維持候選，不阻塞發布 |
| OQ-8（ADR-002） | Pi 版本如何釘選／升級，避免 Issue #24 Gate 1/2/3/4 證據隨版本更新失效 | 升級 Pi 版本前需重跑對應 Gate 的等價測試，不得假設行為不變 |
| OQ-9（ADR-002） | Gate 0（未安裝 Git Bash 的乾淨環境驗證）由誰、何時補測 | 視為 Alpha 1 發布前的待確認事項；`internal/pirpc` 與 taylor-tools.ts 可先在有 Git Bash 的機器上開發，不阻塞其餘實作 |
| OQ-10 | 檔案寫入的 sub-millisecond rename 競態：外部程序在 `internal/filetools` 鎖內重驗 hash 與原子換檔之間以 rename 蓋掉目標檔，會被靜默覆寫（OS 的 byte-range lock 不擋 rename，POSIX flock 為 advisory） | Alpha 1 接受為 best-effort：併發寫入者僅為外部人為編輯，Brunel 內部無併發 writer；命中後果為單次未提交編輯遺失、非損毀、非累積、git 可救。Alpha 3「單一 writer」時重評——屆時若 Brunel 內部出現併發 writer，需加 path-keyed 序列化 |
| OQ-11（Issue #47） | 就近 AGENTS.md 送達後，若 Pi context 被 compaction 擠出，`taylor-tools.ts` 的 per-session 已送集合仍視為已送，不會補送；該 session 內該規則等同永久遺失 | Alpha 1 接受此殘餘落差：`internal/pirpc` 現況不轉譯 `compaction_start`/`compaction_end` event（#9 範圍外），`taylor-tools.ts` 也無此訊號可用於觸發補送。比照 F-10 原裁決立場——AGENTS.md 是 context-only 規則，不影響 Go 端授權結果，此落差是規則遵循品質問題、不是 AC-5 安全不變式缺口，不阻塞 Alpha 1 發布。日後若要修，需先讓 Pi RPC 曝露 compaction 事件給 taylor-tools.ts，屬另開 issue 的範圍 |

未裁決問題不得由實作者自行升級成正式需求。

---

## 17. Roadmap

1. **Alpha 1**：單一主模型閉環、固定工具、事故防護、薄型 TUI、session、context 與客觀完成報告。
2. **Alpha 2**：技能按需載入。
3. **Alpha 3**：Subagent、單一 writer、預算與主模型驗收。
4. **Alpha 4**：本機 smoke／完整 benchmark、拋棄式副本、硬性預算與比較報表。
5. **未定**：MCP、plugin、Taylor connector、LSP、web 工具、ChatGPT OAuth。

---

## 18. 修訂記錄

| 版本 | 日期 | 修改內容 | 作者 |
|---|---|---|---|
| v1.3.2 | 2026-09-18 | 依 Issue #47 裁決（DECISIONS.md 2026-09-18 條目，方向 1）改寫 §7.3 為「root 於啟動注入、子目錄規則於首次觸及該目錄的成功工具結果送達，自該次起對同目錄後續操作生效，首次操作不受此層約束」；AC-5 通過標準同步改寫為「首次操作後、同目錄後續操作時就近規則生效，且不得授權工具」；新增 §16 OQ-11 記錄 context compaction 可能擠掉已送達規則、per-session 已送集合不會補送的殘餘落差，接受為 Alpha 1 已知限制。僅文件對齊 PR #46 之後的實作（`agents-md-delivery.ts`／`taylor-tools.ts` per-session 去重），AC-5 安全不變式本身未變更。 | 使用者裁決 + Claude |
| v1.3 | 2026-08-12 | 依 [ADR-002](adr/ADR-002-pi-agent-runtime.md) 正式修訂：G-1 放棄零依賴單檔 exe；§5 架構改為 Go Host + Pi RPC（`internal/pirpc`），8 個工具維持 Go 實作經 Taylor extension 暴露；§5.3 Provider 委派給 Pi，不再限定 OpenRouter，不再 FROZEN；新增 INV-9（`bash` command 禁止清單）；§7.1 Session 註記 Pi 自身 session 停用；§9 CT-6、§13 TC-PROV→TC-PIRPC、§14、§16 OQ-8／OQ-9 同步更新。§6（安全與事故防護）維持不變，僅註記 Pi 不繞過安全決策入口。 | 使用者裁決 + Claude（wayfinder 三題定案：8 工具全留 Go、session 以 Brunel 為準、provider 開放多家） |
| v1.2 | 2026-07-14 | 將安全定位收斂為事故防護與 AUTO／CONFIRM；加入 Go 1.25 + Bubble Tea v2 薄型 TUI；CompletionReport 改記客觀事實；benchmark runner 移回 Alpha 4；合併重複工程契約為單一來源。 | Codex + 使用者裁決 |
| v1.1 | 2026-07-13 | 補入工程契約、需求矩陣與測試計畫。 | Codex |
| v1.0 | 2026-07-13 | 初版。 | Maze + Claude |
