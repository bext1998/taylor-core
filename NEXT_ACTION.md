# Brunel — 下一步行動

> 最後同步：2026-09-17

## 下一個 Session 目標

#9（F-8：Pi RPC 子行程 ↔ `Agent`／`EventSink` 橋接）核心已完成並送出 [PR #40](https://github.com/bext1998/taylor-core/pull/40)，等待 review／merge，尚未進入 `main`。#4／#29／#30 早先已合併（見 STATUS.md）。可執行前線是 **review PR #40** 與獨立的 **#31**。

## 優先行動

1. **Review 並合併 PR #40**（#9 核心）：新增 `internal/agent`（`Agent`／`EventSink`／`Event` FROZEN 契約＋`Runtime`）、`internal/completion`／`internal/provider`（最小型別，完整填值留給 #14）、`internal/pirpc/runtime.go`＋`launch_windows.go`／`launch_nonwindows.go`（子行程生命週期、RPC event 解碼）。已重跑驗證：`go build`／`vet`／`test` 在 `windows-latest` 與 `GOOS=linux`（ubuntu-latest）兩條路徑通過、`TestTCPIRPC001` 通過、`launch_windows_test.go` 有真實假 `pi` 子行程整合測試。Review 時留意：AGENTS.md 注入目前只做工作區根目錄最小版本（就近載入留給 #11）、`completion.Report` 的 `Diff`／`ToolFailures`／`RemainingRisks` 目前留空或未完整填值（留給 #14）、Approver 實際實作仍屬 #2。合併後才能正式判定 AC-2／AC-6／AC-7／AC-9～AC-11／AC-14。
2. PR #40 合併後排入：#2（含 `safety.Approver` 的 TUI／純文字實作）、#11（AGENTS.md 就近載入完整版）、#14（CompletionReport 完整填值，含 `workspace_diff` 語意 vs `Diff` 欄位的決策——見 [#9 review 交接註解](https://github.com/bext1998/brunel/issues/9#issuecomment-5620455153)，這個決策目前仍未拍板）、#22（E2E fixtures）。
3. **#31**（§6.2 classifier 漏判／過度確認強化，PR #27 事後審查）：無硬阻塞、獨立於 #9，建議 Alpha 1 發布前完成。
4. F-3 收尾（不阻塞前線）：處理 [#4 review 註解](https://github.com/bext1998/brunel/issues/4#issuecomment-5620453317) 的 10 項 minor／nit（`workspace_diff` timeout 混碼＋stderr 汙染＋漏 staged／untracked、`search_text` 無上限、`max_depth:0` 文件化、巢狀 null coerce、缺失路徑錯誤碼、3 個測試缺口、`STATUS.md` 用詞）。另 #29 的 `taylor-tools.ts` 仍待真實 TS 編譯／Pi runtime 驗證、`typebox` 依賴重複、per-call re-bind 無 session root identity（INV-5）——皆屬 #9／PR #40 後續。
5. INV-9 CI 防線的 follow-up（PR #36 review nit）：`go test -run '^TestTCPIRPC001$'` 在守衛測試被刪／改名時退出 0，靜默失去防線；可另讓 CI 斷言該測試存在。
6. 收尾 #4／#5／#7／#8／#29 的 Issue：核心均已合併，剩餘工作由 #9／#2／#31 承接；確認各 Issue 是否隨 #9 一併關閉或先行關閉。
7. 規劃 #2 前將 Go module 基線由 1.22 同步至 1.25.x 並引入 Bubble Tea v2；此項需另行實作授權，與 Route B 無關（Host 層仍是 Go）。
8. 觀察到 `internal/exec` 的 `TestPSRunner_Timeout_KillsProcessTree` 在高負載下偶發 flaky（見 STATUS.md「已知驗證限制」）；非本次變更引入，之後有空可排查是否要加寬時間容忍度。

## 阻塞與待決策

- 無 Alpha 1 硬阻塞；`docs/spec.md` §5／§9 的 Route B 修訂已於 v1.3 完成。
- Gate 0（物理上無 Git Bash 的環境）補測：依使用者裁決不另建 Issue，維持 ADR-002 現況——Git for Windows 為已文件化安裝依賴，spec OQ-9 視為接受風險、不驗證。
- OQ-8（Pi 版本釘選與升級前 Gate 重跑政策）尚未建 Issue；升級 Pi 版本前需重跑對應 Gate 等價測試。
- OQ-10（檔案寫入的 sub-millisecond rename 競態）：Alpha 1 已裁決接受為 best-effort（見 DECISIONS.md 2026-09-08）；Alpha 3「單一 writer」時重評，屆時若 Brunel 內部出現併發 writer 需加 path-keyed 序列化。
- 公開錯誤不含 secret 的最終責任邊界：Pi 自行探索、Brunel 從未持有的 provider key 若被 Pi 回顯於錯誤訊息，`internal/pirpc` 只能做啟發式遮罩（`internal/redact` 已能在 Brunel 持有實際值時精確替換）。責任歸屬需在 spec 或 #9 定義。
- spec §16 其餘 Open Questions 依各自裁決前行為處理。

## 參考

- `docs/spec.md` §4～§6、§8～§16
- `docs/adr/ADR-002-pi-agent-runtime.md`
- `MAZE_PROJECT.md`
