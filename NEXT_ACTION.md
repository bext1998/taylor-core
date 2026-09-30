# Brunel — 下一步行動

> 最後同步：2026-10-01

## 下一個 Session 目標

讓新使用者第一次使用就能正常啟動：修正 OpenRouter key 的存取（[#65](https://github.com/bext1998/taylor-core/issues/65)）。`cmdkey` 存入的 UTF-16 密碼被當 UTF-8 讀出 NUL，注入環境變數後 Pi 子行程無法啟動，使用者只看到看不出原因的 `E_RUNTIME_REQUIRED`；README 也沒有說明如何設定 key。

## 優先行動

1. **#65**：讀取端處理或明確拒絕 UTF-16 blob（做法需先裁決）；含 NUL 的 key 不得進入環境區塊；`CreateProcess` 失敗訊息帶可公開的 Win32 原因；再補 README 的 key 設定步驟（是否新增設定入口需裁決）。補 UTF-16／UTF-8 回歸測試。
2. **#14**（客觀 CompletionReport 1.0）：目前 `--report` 的 `modified_files`、`diff`、`verifications` 為空，#2 的 AC-3 與 #54、#55 都依賴它。
3. **#54**：無 TTY 遇需確認命令時立即以非零狀態結束 run。已在真實 Pi 重現：退出碼為 0，且模型在第一個命令被拒後會繼續嘗試後續命令。

## 阻塞與待決策

- **#8 卡在 OQ-12**：Pi 自行探索、Brunel 從未持有的 provider key 若被回顯，公開錯誤不含 secret 的責任邊界未定義。真實 Pi 下已看到 OpenAI 回顯部分遮罩的 key（前綴加末 4 碼）並原樣通過。裁決前 #8 不視為已解決。
- **#11 卡在 OQ-13**：就近 AGENTS.md 的 4 個邊界案例需使用者裁決（時序語意已由 [#47](https://github.com/bext1998/taylor-core/issues/47) 裁決採方向 1，#47 已關閉）。
- **#2 的 AC-3 範圍**：「`--report` 的 JSON 完整」算 #14 還是 #2 要等它，需使用者裁決；AC-1 未逐條驗證。
- **#29**：尚未確認每次呼叫皆為新程序時的 session root identity 一致性（INV-5）與頂層／Pi 底下兩份 `typebox` 是否衝突；TS 測試（`npm test`）已於 2026-10-01 全數通過。
- **#2、#8、#11、#29 是否關閉**：屬治理判斷，需 AC、QA、CI、文件與 PR 合併皆完備；#4、#5、#7、#9、#49、#67 已關閉。
- 其他前線（不阻塞 Alpha 1）：#22（待 #14）、#31、#52、#53、#55、#57、#61～#63；#69 為 #4 的 minor 備忘清單。
- 無 Alpha 1 硬阻塞；spec §16 其餘 Open Questions 依各自裁決前行為處理。

## 參考

- `docs/spec.md` §4～§6、§8～§16
- `docs/adr/ADR-002-pi-agent-runtime.md`
- `DECISIONS.md`
- `MAZE_PROJECT.md`
