# Taylor-core 代號：Brunel

Brunel 是一個面向 Windows x64 的實驗性 coding harness，用來驗證：當模型具備足夠的自主推理與工具使用能力時，harness 是否能聚焦於工具、邊界、透明度與完成證據，而不需要強制固定工作流。

## 專案狀態

目前處於 Alpha 1 初期實作階段。正式需求、架構不變式、凍結介面與驗收條件請參閱 [`docs/spec.md`](docs/spec.md)。

## 技術環境

- Go 1.22（目前實作基線；Alpha 1 v1.2 目標基線為 Go 1.25.x，TUI 實作前需同步）
- Windows x64
- PowerShell 7 (`pwsh`)
- Node.js/npm 與 Git for Windows（[ADR-002](docs/adr/ADR-002-pi-agent-runtime.md)：model-facing Agent Runtime 委派給 [Pi](https://github.com/earendil-works/pi)，經 `pi --mode rpc` 呼叫）
- `CGO_ENABLED=0` 靜態編譯

## Model Provider

Brunel 不自行實作 provider 選擇、SSE streaming、tool-call probe 或重試邏輯——這些都委派給 Pi（`internal/pirpc`）。**實際可用的 provider／model 範圍等於使用者當下安裝的 Pi 版本所支援的範圍，會隨 Pi 版本變動，Brunel 不承諾涵蓋任何特定清單。** `--model`（可含 provider 前綴，語法依 Pi 慣例）與可選的 provider 會直接透傳給 `pi --mode rpc` 的啟動參數；Pi 回報的 provider 層錯誤（認證、額度、模型不存在、協定錯誤）會被轉譯為 Brunel 自己的錯誤碼顯示，但 Brunel 不會自行重試或覆蓋 Pi 已決定的重試／放棄行為。

API key 一律優先存於 Windows Credential Manager，並在啟動 Pi 子行程時經環境變數注入（例如 `OPENROUTER_API_KEY`，比照 Pi 自己已支援的憑證機制），不會寫入任何專案設定檔。注入時 key 會與其來源 provider 綁定，provider 不符則拒絕注入。目前 Brunel 只從 Credential Manager 解析 **OpenRouter** key；其他 provider 的憑證需由使用者自行設定，交由 Pi 既有的 credential 探索機制（`settings.json` 或既有環境變數）處理。

## 開發指引

- Coding Agent 先閱讀 `AGENTS.md`、`MAZE_PROJECT.md`、`STATUS.md` 與 `NEXT_ACTION.md`。
- 不得自行修改規格中的 `[FROZEN]` 契約；變更須走規格修訂與使用者裁決。
- 後續變更使用功能分支與 Pull Request，不直接推送 `main`。

## Session 資料安全

Session 會以未加密檔案保存在本機。Brunel 會在寫入前遮罩已知的 API key、Authorization header 與 `.env` 憑證模式，但這僅是 best-effort，無法保證辨識所有敏感內容；API key 與 token 不得寫入 Session，正式憑證只由 Windows Credential Manager 提供。

## 授權

本專案採用 [Apache License 2.0](LICENSE)。
