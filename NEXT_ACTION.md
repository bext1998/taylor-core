# Brunel — 下一步行動

> 最後同步：2026-09-17

## 下一個 Session 目標

#9（F-8：Pi RPC 子行程 ↔ `Agent`／`EventSink` 橋接）核心已透過 [PR #40](https://github.com/bext1998/taylor-core/pull/40) 合併至 `main`（commit `c620351`），經 codex 三輪隔離審查修正（settle 分類改以 `message_end.stopReason` 為準、session 寫入失敗一律回報 `failed`、Windows handle 洩漏與 `toolcall_end` 解碼錯誤）。#11（F-10 就近 AGENTS.md 與不可提權保護）核心已透過 [PR #46](https://github.com/bext1998/taylor-core/pull/46) 合併至 `main`（**部分實作**，merge commit `9842177`）：經 Codex 兩輪合併前審查（symlink 讀取 blocker 改 `os.Lstat` 修復；時序語意差距轉 [#47](https://github.com/bext1998/taylor-core/issues/47) 追蹤）。#4／#5／#7／#8／#29／#30 均已落地。可執行前線收斂為 **#2／#14／#22**（皆已解除 #9 阻塞）與獨立的 **#31**，另 **#47**（F-10「操作前」時序語意的產品層裁決）。**真實 `pi --mode rpc` 端到端仍未驗證**，目前只有 `internal/pirpc/testdata/fakepi` 的子行程整合測試。

## 優先行動

1. 排入 **#2**（含 `safety.Approver` 的 TUI／純文字實作，Go module 需先由 1.22 同步至 1.25.x 並引入 Bubble Tea v2，這項需另行實作授權）、**#14**（CompletionReport 完整填值；`internal/completion.Report` 型別已就緒但 `Diff`／`ToolFailures`／`RemainingRisks` 大多留空，需先裁決 `workspace_diff` 語意 vs `Diff` 欄位——見 [#9 review 交接註解](https://github.com/bext1998/brunel/issues/9#issuecomment-5620455153)）、**#22**（E2E fixtures，待其餘項目就緒）。#11 核心已落地（PR #46），剩餘工作由 **#47**（時序語意裁決，見下）與真實 Pi E2E 承接。
2. **#47**（F-10 時序語意裁決，產品層）：PR #46 的實際語意是規則隨「觸及該目錄的第一個 tool result」送達，spec §7.3「操作前按需讀取」未完全達成；#47 記錄三個候選方向（接受操作後送達／新 RPC context 注入／兩段式工具呼叫）與驗收條件，需裁決擇一。
3. **在排入 #2 之前，安排一次真實 `pi --mode rpc` 端到端驗證**（需要實際安裝 Node.js/npm 與 pi CLI 的環境）：目前 PR #40 的所有驗證都停在 fakepi 假子行程層級，AC-2／AC-6／AC-7／AC-9～AC-11／AC-14 都還不能算正式判定通過；PR #46 的 F-10 行為（就近規則隨 tool result 送達）同樣需在此 E2E 中驗證。
4. **#31**（§6.2 classifier 漏判／過度確認強化，PR #27 事後審查）：無硬阻塞、獨立進行，建議 Alpha 1 發布前完成。
5. F-3 收尾（不阻塞前線）：處理 [#4 review 註解](https://github.com/bext1998/brunel/issues/4#issuecomment-5620453317) 的 10 項 minor／nit（`workspace_diff` timeout 混碼＋stderr 汙染＋漏 staged／untracked、`search_text` 無上限、`max_depth:0` 文件化、巢狀 null coerce、缺失路徑錯誤碼、3 個測試缺口、`STATUS.md` 用詞）。另 #29 的 `taylor-tools.ts` 仍待真實 TS 編譯／Pi runtime 驗證、`typebox` 依賴重複、per-call re-bind 無 session root identity（INV-5）。
6. INV-9 CI 防線的 follow-up（PR #36 review nit）：`go test -run '^TestTCPIRPC001$'` 在守衛測試被刪／改名時退出 0，靜默失去防線；可另讓 CI 斷言該測試存在。
7. 收尾 #4／#5／#7／#8／#9／#11／#29 的 Issue：核心均已合併，剩餘工作由 #2／#14／#47／#31 承接；確認各 Issue 是否隨對應後續工作一併關閉或先行關閉。
7. **流程提醒（2026-09-17 使用者裁決）**：STATUS.md／NEXT_ACTION.md 的同步只在一個 PR 的 review 全部跑完（合併或確定關閉）之後才做，不要在 PR 剛開、審查還在進行中就先同步——PR #41 曾在 #40 剛送審時就先寫「等待 review」，結果 #40 又經過三輪修正才真的可合併，#41 內容很快過期，被使用者關閉重做。

## 阻塞與待決策

- **#47（F-10 時序語意）待產品層裁決**：三個候選方向（接受操作後送達並更新 spec §7.3 表述／新 RPC context 注入指令／兩段式工具呼叫）擇一；裁決前 #11 維持「部分實作」狀態。
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
