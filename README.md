# Taylor Core 代號：Brunel

Brunel 是一個面向 Windows x64 的實驗性 coding harness，用來驗證：當模型具備足夠的自主推理與工具使用能力時，harness 是否能聚焦於工具、邊界、透明度與完成證據，而不需要強制固定工作流。

## 專案狀態

目前處於 Alpha 1 初期實作階段。正式需求、架構不變式、凍結介面與驗收條件請參閱 [`docs/spec.md`](docs/spec.md)。

## 技術環境

- Go 1.25.x（`go.mod`：`go 1.25.0`；TUI 使用 Bubble Tea v2）
- Windows x64
- PowerShell 7 (`pwsh`)
- Node.js/npm 與 Git for Windows（[ADR-002](docs/adr/ADR-002-pi-agent-runtime.md)：model-facing Agent Runtime 委派給 [Pi](https://github.com/earendil-works/pi)，經 `pi --mode rpc` 呼叫）
- `CGO_ENABLED=0` 靜態編譯

## 安裝與部署

先安裝 Node.js **22.19.0 以上**（含 npm）、PowerShell 7 與 Git for Windows。在 Brunel repository 根目錄執行：

```powershell
npm ci
go build -o brunel.exe ./cmd/brunel
```

`npm ci` 是必要步驟，會依 `package-lock.json` 安裝精確鎖定的 **Pi 0.85.1** 與相依套件。不要以 `npm install -g` 安裝 Pi 作為部署步驟。

`brunel.exe`、`taylor-tools.ts`、`agents-md-delivery.ts`、`package.json`、`package-lock.json` 與 `node_modules/` 應放在同一安裝目錄；也可將前五個檔案複製到部署目錄後，在該目錄重新執行 `npm ci`。從任意目標 workspace 執行該目錄下的 `brunel.exe` 即可。

啟動時優先從 extension 所在目錄、再從 `brunel.exe` 所在目錄尋找 `node_modules/@earendil-works/pi-coding-agent/package.json`，讀取 `bin.pi`，以 `node <cli.js>` 啟動，避開 Windows `.cmd` shim。本機套件缺失時才查找 PATH 上的 `pi`（相容既有安裝）；本機套件損壞會直接失敗，不會改用全域版本。

每次啟動 RPC 前都會執行同一入口的 `--version`，僅接受 `0.85.1`。版本不符回傳 `E_PI_VERSION_MISMATCH`；缺少 Pi／Node 或無法查詢版本回傳 `E_RUNTIME_REQUIRED`，可先重新執行 `npm ci`。本機版本存在時，全域 Pi 的版本不影響選擇。

升級 Pi 必須同步 `package.json`、lockfile 與 `internal/pirpc.SupportedPiVersion`，並重跑對應 Gate 等價測試（spec §16 OQ-8）；本次沿用既有版本，正式升級 checklist 仍屬後續工作。

## 使用方式

```text
brunel [flags]              在終端（TTY）中啟動互動 TUI
brunel [flags] "<task>"     以純文字模式執行單次任務（不進入 alternate screen）

--mode workspace|readonly   預設 workspace；readonly 直接拒絕寫入工具與 PowerShell
--model <id>                原樣透傳給 pi --model，例如 openrouter/<model>
--name <name>               為 session 命名（命名的 session 會保留）
--resume <name|id>          恢復既有 session
--report <path>             寫出 CompletionReport JSON（僅純文字模式；檔案不得已存在、必須在 workspace 內，不覆寫）
```

旗標可放在 task 前後。模型也可在 `<workspace>\.brunel\config.json` 或 `%USERPROFILE%\.brunel\config.json` 以 `model_id` 設定。

- **TUI**：Enter 送出、Ctrl+J（或 Shift+Enter）換行、PgUp／PgDn 捲動 transcript；執行中按 Ctrl+C 取消，閒置時按 Ctrl+C 離開。需要確認的命令會跳出 modal 顯示命令與原因，按 `y` 批准本次、`n`／Esc 拒絕。
- **純文字模式**：模型回覆寫到 stdout，工具活動與提示寫到 stderr。stdin 是終端時，需要確認的命令會在終端詢問 `[y/N]`；stdin 不是終端（例如 pipe）時，需要確認的命令直接以 `E_APPROVAL_REQUIRED_NO_TTY` 拒絕。
- **退出碼**：`0` 任務完成（completed）、`1` 失敗或未完成（failed／incomplete）、`2` 參數錯誤（`E_INVALID_ARGUMENT`）。
- `taylor-tools.ts`（Pi extension）需放在 `brunel.exe` 同一目錄。

## Model Provider

Brunel 不自行實作 provider 選擇、SSE streaming、tool-call probe 或重試邏輯——這些都委派給 Pi（`internal/pirpc`）。**實際可用的 provider／model 範圍等於使用者當下安裝的 Pi 版本所支援的範圍，會隨 Pi 版本變動，Brunel 不承諾涵蓋任何特定清單。** `--model`（可含 provider 前綴，語法依 Pi 慣例）與可選的 provider 會直接透傳給 `pi --mode rpc` 的啟動參數；Pi 回報的 provider 層錯誤（認證、額度、模型不存在、協定錯誤）會被轉譯為 Brunel 自己的錯誤碼顯示，但 Brunel 不會自行重試或覆蓋 Pi 已決定的重試／放棄行為。

API key 一律優先存於 Windows Credential Manager，並在啟動 Pi 子行程時經環境變數注入（例如 `OPENROUTER_API_KEY`，比照 Pi 自己已支援的憑證機制），不會寫入任何專案設定檔。注入時 key 會與其來源 provider 綁定，provider 不符則拒絕注入。目前 Brunel 只從 Credential Manager（target `Brunel/OpenRouter`）解析 **OpenRouter** key，而且只有模型實際走 OpenRouter 時才需要它；其他 provider 的憑證需由使用者自行設定，交由 Pi 既有的 credential 探索機制（`settings.json` 或既有環境變數）處理。

### 設定 OpenRouter key

```powershell
brunel login            # 在終端機以隱藏輸入貼上 key
brunel logout           # 移除已存的 key
```

- 比照 Pi 的 `/login`、`/logout`：`brunel login [openrouter]` 只處理 OpenRouter；其他 provider 請用 Pi 自己的 `/login`。
- key 只從隱藏提示（終端機）或 stdin 第一行（管線，例如從密碼管理器的輸出接入）讀取，**不接受參數**，也不會被印出或寫入 Session，避免留在 shell 歷史與程序清單。不要把 key 寫進專案檔或貼進對話。
- 檢查是否已存入：`cmdkey /list:Brunel/OpenRouter`（只列出條目，不顯示 key）。更換 key 再執行一次 `brunel login`。
- 用 `cmdkey` 或 Windows 認證管理員圖形介面存入的 key（UTF-16）也能讀取；若存入內容無法解讀為文字，Brunel 會在啟動前以 `E_CONFIG_CREDENTIAL` 拒絕並提示重新執行 `brunel login`。
- 若真的要把單字 `login` 當成任務，寫 `brunel -- login`。

## 開發指引

- Coding Agent 先閱讀 `AGENTS.md`、`MAZE_PROJECT.md`、`STATUS.md` 與 `NEXT_ACTION.md`。
- 不得自行修改規格中的 `[FROZEN]` 契約；變更須走規格修訂與使用者裁決。
- 後續變更使用功能分支與 Pull Request，不直接推送 `main`。
- E2E fixtures（AC-16）在 `e2e/`：bug 修復、小功能、失敗測試診斷三個小型 Go 專案，各含任務、驗證命令與參考解答。預設 `go test ./...` 只驗證 fixture 本身（修正前失敗、套用參考解答後通過）；對真實模型的閉環檢查需自行啟用，不跑在預設 CI：在專案根目錄建出 `brunel.exe`，設定 `BRUNEL_E2E_EXE`（其路徑）與 `BRUNEL_E2E_MODEL`（`--model` 值），再執行 `go test ./e2e -run RealModel -v`。

## Session 資料安全

Session 會以未加密檔案保存在本機。Brunel 會在寫入前遮罩已知的 API key、Authorization header 與 `.env` 憑證模式，但這僅是 best-effort，無法保證辨識所有敏感內容；API key 與 token 不得寫入 Session，正式憑證只由 Windows Credential Manager 提供。

## 授權

本專案採用 [Apache License 2.0](LICENSE)。
