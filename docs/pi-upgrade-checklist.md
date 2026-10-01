# Pi 升級 Checklist

> 對應 spec §16 OQ-8（升級 Pi 前須重跑對應 Gate 的等價測試，不得假設行為不變）與 Issue #61。
> 目前支援版本：`SupportedPiVersion`（`internal/pirpc/version.go`），釘選於 `package.json` 與 `package-lock.json`。

## 原則

- **候選版本在全部步驟通過前，不是支援版本，也不得合併進 `main`。** 驗證本身需要 Brunel 接受候選版本（`checkPiVersion` 在啟動 RPC 前會拒絕與 `SupportedPiVersion` 不同的 Pi），所以版本常數**在升級分支的第一步就改**；「`main` 不接受未驗證的候選」由合併前的證據要求維持，不靠延後改常數，也不要新增繞過版本檢查的設定。
- 本清單只涵蓋 Pi 版本變動。換 runtime、改 RPC 協定設計、fork Pi 不在範圍（ADR-002 的 Route C）。
- 每個步驟的結果寫進升級 PR 的描述（見「證據」），不留口頭結論。

## 責任分工：誰驗證什麼

| 層 | 內容 | 由誰／何時 |
|---|---|---|
| CI（Windows target、Ubuntu portability guard） | `go vet`、`go build`、`go test`（含 `BRUNEL_TEST_REAL_PI=1` 的真實 Pi 啟動 smoke `TestStartLocalInstalledPi`）、INV-9 靜態檢查、零 CGO 建置、Windows 上 `npm ci` | 每個 PR 自動 |
| 升級者本機 | `npm test`（**CI 不跑**）、RPC 事件擷取比對、真實模型／工具 E2E、行程殘留檢查 | 升級者，合併前 |
| 發布驗收 | spec §13 的 AC 彙整 | 發布前，不屬升級 PR |

**get_state 級的啟動 smoke 不等於 E2E。** `TestStartLocalInstalledPi` 只證明本機 Pi 能啟動並回應 RPC；模型實際呼叫 8 個工具的完整閉環（Issue #49 的範圍）必須由升級者另跑，結果記在 PR。

## 步驟

### 1. 選定候選版本與準備

- [ ] 在 npm 上確認候選版本、發布日期與變更紀錄；挑具體版本，不用範圍。
- [ ] 開升級分支；`npm install --save-exact @earendil-works/pi-coding-agent@<候選版本>`，確認 `package.json`、`package-lock.json` 都是精確版本（`TestPiVersionPinMatchesManifests` 會檢查兩者與 `SupportedPiVersion` 一致，Pi 必須是直接依賴而非 peer）。
- [ ] 同一個分支同步 `SupportedPiVersion`（`internal/pirpc/version.go`），並更新依賴舊版本的測試資料：fakepi 的預設 `--version` 回傳值（`internal/pirpc/testdata/fakepi/main.go`，現為寫死的版本字串）、`TestStartRejectsUnverifiedPiBeforeRPC` 的拒絕清單（要保留**與候選版本不同**的拒絕樣本）。否則換常數後會因舊測試資料失敗，那不代表候選 runtime 不相容。
- [ ] 建出候選版本的 exe：在儲存庫根目錄 `go build -o brunel.exe ./cmd/brunel`（`taylor-tools.ts` 與 `node_modules` 需在它旁邊）。後面所有 E2E 都指向這個新建的 exe；`BRUNEL_E2E_EXE` 若指向舊 exe，會由舊的版本守衛判斷，證據無效。
- [ ] 記下 lockfile 中該套件的 `integrity`（`package-lock.json` 的 `node_modules/@earendil-works/pi-coding-agent` 項）。
- [ ] 檢查套件的 `bin.pi` 路徑是否改變（`piPackageEntry` 依它啟動 Node）。

### 2. RPC 協定差異檢查

- [ ] 讀候選版本套件內 `docs/` 的 RPC 文件與 CHANGELOG：新增或改名的 RPC command／event、`--mode rpc`、`--no-builtin-tools`、`--no-extensions`、`-e`、`--no-session` 的語意是否變動。
- [ ] 以真實 Pi 擷取原始事件，與既有測試中的真實樣本比對：對暫存 workspace 以 `node <pi 入口> --mode rpc --no-builtin-tools --no-extensions -e <taylor-tools.ts> --no-session --model <模型>` 啟動，並設 `BRUNEL_EXE`、`BRUNEL_MODE`；從 stdin 送一行 `{"type":"prompt","message":"..."}`，讓模型各呼叫工具一次，保存 stdout。重點看 `tool_execution_start` 的 `args`、`tool_execution_end` 的 `result.details`／`isError`、`message_end` 的 `stopReason`、`agent_settled`。
- [ ] 若事件形狀有變，更新 `internal/pirpc/tool_events_test.go` 與 `runtime_test.go` 內取自真實 Pi 的樣本，再改 `decodeRPCEvent`；不要只改解碼而不更新樣本。

### 3. 對應原 Gate 的驗證（Issue #24／ADR-002）

| Gate | 要證明什麼 | 測試與判定 |
|---|---|---|
| **Gate 1 Model Tool Authority** 工具 authority | 模型只能用 Brunel 註冊的 8 個工具，Pi 內建工具與其他 extension 都關閉；所有 I/O 與安全裁決仍在 Go | `TestBuildArgsMatchesFrozenCommandLineWithProvider`（啟動參數仍含 `--no-builtin-tools --no-extensions -e`）；`npm test`（extension 恰註冊 8 個工具）；`TestParameterSchemaSnapshot`；`TestRegistryRequiresGateBeforeEveryTool`；真實 E2E 中 8 個工具皆被呼叫成功。**判定**：全過，且 E2E 沒有出現 Pi 內建工具名稱 |
| **Gate 2 RPC side channel** | Brunel 的 RPC client 不送 `{"type":"bash"}`；新版本沒有新增可執行命令的 host-level RPC command 被我們用到 | `go test ./internal/pirpc -run '^TestTCPIRPC001$'`（INV-9 靜態檢查，CI 也跑）；步驟 2 的文件檢查。**判定**：INV-9 守衛不得放寬；若新版本新增類似 command，只能在 spec 修訂後使用 |
| **Gate 3 Windows process authority** | Job Object 與 Pi 行程樹獨立；Pi abort／crash／取消後不留任何子孫程序 | `TestStartPiProcessLifecycle`、`TestCloseIsIdempotent`、`TestVersionProbeReclaimsItsWholeProcessTree`、`TestRunAbortsOnContextCancel`；以步驟 1 建出的 Brunel 在本機跑一次取消（plain 模式 Ctrl+C）：執行中先記下**本次** Brunel、Pi（node）與其子孫（例如 `pwsh`）的 PID，用 `Get-CimInstance Win32_Process` 依 `ParentProcessId` 追出這組行程；取消處理與 Brunel 退出完成後，確認**這組 PID 都已消失**。不要以「全機沒有 node」判定（會把別的工具或別的 session 算進去）。注意直接對 Pi 送 RPC abort 只會讓 Pi 的 session 回到 idle，不能當成 Brunel 已終止程序樹的證據；程序樹的終止是 Brunel 的 `PiProcess.Abort`／Job Object。**判定**：本次那組 PID 殘留數為 0 |
| **Gate 4 雙 runtime 複雜度** | Pi RPC → TypeScript extension → Go Host → PowerShell 的完整閉環仍可行，取消／串流／錯誤不需要額外 glue | 真實模型／工具 E2E：`BRUNEL_E2E_EXE`、`BRUNEL_E2E_MODEL` 後 `go test ./e2e -run RealModel -v`（三個 fixture）加一次含批准流程的 TUI 或 plain 實跑；錯誤轉譯 `TestTranslateProviderErrorRealWorldMessages`。**判定**：三個 fixture 全過；因版本變動而必要的事件解碼／extension 適配要記錄在 PR；若需要擴大跨 runtime 的架構或責任（ADR-002 的推翻條件是整合成本遠高於 spike 粗估、成為龐大的 Go↔TypeScript bridge），回報使用者，不自行吞下 |
| **Gate 0 Windows runtime**（Git Bash） | 依 DECISIONS.md 2026-09-08：Git for Windows 為已文件化依賴，**不補測**「物理上無 Git Bash」 | 不重測；若新版本移除或新增對 shell 的依賴，須在步驟 2 記錄並回報使用者 |

### 4. 版本檢查與啟動行為

（`SupportedPiVersion` 已在步驟 1 同步。）

- [ ] `go test ./internal/pirpc`：`TestResolvePiLocalBeforePATH`、`TestResolvePiGlobalAndMissing`（本機優先、缺失 runtime）、`TestStartRejectsUnverifiedPiBeforeRPC`（版本不符在進入 RPC 前被拒、不洩漏探測輸出）、`TestStartLocalInstalledPi`（需 `npm ci` 與 `BRUNEL_TEST_REAL_PI=1`）。
- [ ] 錯誤碼維持 spec EC-13：缺 runtime／查詢失敗 `E_RUNTIME_REQUIRED`、版本不符 `E_PI_VERSION_MISMATCH`。

### 5. 全套與部署

- [ ] `npm ci` 後 `go vet ./...`、`go build ./...`、`go test ./...`（`BRUNEL_TEST_REAL_PI=1`）、`npm test`；零 CGO 建置；Windows／Ubuntu CI 皆綠。
- [ ] README「安裝與部署」的 Pi 版本與部署步驟仍正確（`brunel.exe` 旁需有 `taylor-tools.ts` 與 `node_modules`）。

## 證據（貼在升級 PR 描述）

```
Pi：<舊版本> → <新版本>（發布日 <日期>）
lockfile integrity：<sha512-...>
RPC 差異：<無 / 列出事件或旗標變動與因應>
Gate 1：<測試名稱與結果；E2E 中被呼叫的 8 個工具>
Gate 2：TestTCPIRPC001 <結果>；新增 RPC command：<無/有>
Gate 3：<測試結果>；取消後殘留行程：<數量>
Gate 4：e2e 三個 fixture <結果>；批准流程實跑 <結果>
Gate 0：不重測（DECISIONS 2026-09-08）
CI：<run 連結>；npm test：<結果>
```

## 回復

升級後發現問題，回到前一個支援版本：

1. `git revert` 升級 PR（或把 `package.json`、`package-lock.json`、`SupportedPiVersion` 還原到前一版）。
2. `npm ci` 重新安裝，確認 `TestPiVersionPinMatchesManifests` 與 `go test ./internal/pirpc` 通過。
3. 在 Issue／PR 記錄回復原因與失敗證據，候選版本標為「未通過」，不得留在 `main`。
