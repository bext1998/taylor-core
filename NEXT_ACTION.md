# Brunel — 下一步行動

## 2026-09-30 Session 交接

- #43 實作提交 `78eceaa` 已推送至 `maze/2026-09-30-eeff375`，並建立 [PR #59](https://github.com/bext1998/taylor-core/pull/59)。實作沿用 Pi `0.85.1`，本機 manifest bin 直接由 Node 啟動、啟動前嚴格版本檢查，部署需 `npm ci`。
- 下一步確認 PR #59 的 Windows／Ubuntu CI 結果並完成審查；PR 合併與驗收證據完備前保持 #43 開放。GitHub `workflow` 權限阻塞已解除；本機真實 Pi `Start`／`get_state` smoke 通過，完整模型／工具閉環仍由 #49 追蹤。

> 最後同步：2026-09-30

## 下一個 Session 目標

#2（F-1 CLI、薄型 TUI 與 TTY 契約，含 named pipe 批准通道）核心已透過 [PR #56](https://github.com/bext1998/taylor-core/pull/56) 合併至 `main`（merge commit `0c86a4c`，`Related to #2`）。#4／#5／#7／#8／#9／#11／#29／#30 先前均已落地。**真實 `pi --mode rpc` 端到端仍未驗證**（只有 `internal/pirpc/testdata/fakepi` 的子行程整合測試），且 #2 的 AC 還缺人工 TTY 驗證，因此 #2 不能打勾或關閉。可執行前線：**#49**（真實 Pi E2E）、#2 的後續 **#52**～**#55**、**#14**、**#22**、**#31**、**#47**（F-10 時序語意裁決）、**#57**（Windows CI 偶發失敗）。

## 優先行動

1. **安排一次真實 `pi --mode rpc` 端到端驗證（#49）**（需要實際安裝 Node.js/npm 與 pi CLI 的 Windows 環境），並在同一輪做 **人工 TTY 操作**（TUI 與純文字模式、批准 modal 經 named pipe 的完整流程、長命令捲讀、Ctrl+C）。PR #56、#40、#46 的驗證都停在 fakepi／單元測試層級，AC-2／AC-6／AC-7／AC-9～AC-11／AC-14 都還不能算正式判定通過；#11 的就近規則隨 tool result 送達也需在此驗證。完成後才能判斷 #2 是否可關閉。部署佈局（`taylor-tools.ts` 與 `brunel.exe` 的相對位置）屬 #43／#49。
2. **#47**（F-10 時序語意裁決，產品層）：PR #46 的實際語意是規則隨「觸及該目錄的第一個 tool result」送達，spec §7.3「操作前按需讀取」未完全達成；#47 記錄三個候選方向（接受操作後送達／新 RPC context 注入／兩段式工具呼叫）與驗收條件，需裁決擇一。
3. **#2 後續 Issue**：#54（無 TTY 遇需確認命令時立即以非零狀態結束 run，需與 #14 協調 `pending_approval`）、#52（TUI 同一 session 多次任務的 context 延續）、#53（`--resume` 重建 context）、#55（`--report` 的 CT-8 路徑前置條件，銜接 #14）。另有未處理的小項：子行程被取消後 TUI 上已開的批准 modal 不會自動關閉（無安全影響）。
4. **#31**（§6.2 classifier 漏判／過度確認強化，PR #27 事後審查）：無硬阻塞、獨立進行，建議 Alpha 1 發布前完成。
5. F-3 收尾（不阻塞前線）：處理 [#4 review 註解](https://github.com/bext1998/brunel/issues/4#issuecomment-5620453317) 的 10 項 minor／nit（`workspace_diff` timeout 混碼＋stderr 汙染＋漏 staged／untracked、`search_text` 無上限、`max_depth:0` 文件化、巢狀 null coerce、缺失路徑錯誤碼、3 個測試缺口、`STATUS.md` 用詞）。另 #29 的 `taylor-tools.ts` 仍待真實 TS 編譯／Pi runtime 驗證、`typebox` 依賴重複、per-call re-bind 無 session root identity（INV-5）。
6. INV-9 CI 防線的 follow-up（PR #36 review nit）：`go test -run '^TestTCPIRPC001$'` 在守衛測試被刪／改名時退出 0，靜默失去防線；可另讓 CI 斷言該測試存在。
7. **#57**（Windows CI 的 `TestPSRunner_Timeout_KillsProcessTree` 偶發失敗）：先取得失敗原因證據，再調整測試；不得以放寬斷言或跳過測試取得綠燈。
8. 收尾 #2／#4／#5／#7／#8／#9／#11／#29 的 Issue：核心均已合併，剩餘工作由 #49／#52～#55／#14／#47／#31 承接；確認各 Issue 是否隨對應後續工作一併關閉或先行關閉（關閉屬治理判斷，需 AC、QA、CI、文件與 PR 合併皆完備）。
9. **流程提醒（2026-09-17 使用者裁決）**：STATUS.md／NEXT_ACTION.md 的同步只在一個 PR 的 review 全部跑完（合併或確定關閉）之後才做，不要在 PR 剛開、審查還在進行中就先同步——PR #41 曾在 #40 剛送審時就先寫「等待 review」，結果 #40 又經過三輪修正才真的可合併，#41 內容很快過期，被使用者關閉重做。

## 阻塞與待決策

- **#47（F-10 時序語意）待產品層裁決**：三個候選方向（接受操作後送達並更新 spec §7.3 表述／新 RPC context 注入指令／兩段式工具呼叫）擇一；裁決前 #11 維持「部分實作」狀態。
- 無 Alpha 1 硬阻塞；`docs/spec.md` §5／§9 的 Route B 修訂已於 v1.3 完成。
- Gate 0（物理上無 Git Bash 的環境）補測：依使用者裁決不另建 Issue，維持 ADR-002 現況——Git for Windows 為已文件化安裝依賴，spec OQ-9 視為接受風險、不驗證。
- OQ-8（Pi 版本釘選與升級前 Gate 重跑政策）由 [#43](https://github.com/bext1998/taylor-core/issues/43) 追蹤（鎖定 Pi 版本並改為專案本機安裝）；升級 Pi 版本前需重跑對應 Gate 等價測試。
- OQ-10（檔案寫入的 sub-millisecond rename 競態）：Alpha 1 已裁決接受為 best-effort（見 DECISIONS.md 2026-09-08）；Alpha 3「單一 writer」時重評，屆時若 Brunel 內部出現併發 writer 需加 path-keyed 序列化。
- 公開錯誤不含 secret 的最終責任邊界：Pi 自行探索、Brunel 從未持有的 provider key 若被 Pi 回顯於錯誤訊息，`internal/pirpc` 只能做啟發式遮罩（`internal/redact` 已能在 Brunel 持有實際值時精確替換）。責任歸屬需在 spec 或 #9 定義。
- spec §16 其餘 Open Questions 依各自裁決前行為處理。

## 參考

- `docs/spec.md` §4～§6、§8～§16
- `docs/adr/ADR-002-pi-agent-runtime.md`
- `MAZE_PROJECT.md`
