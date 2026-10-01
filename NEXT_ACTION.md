# Brunel — 下一步行動

> 最後同步：2026-10-02

## 下一個 Session 目標

把 Alpha 1 剩下的**待裁決事項**一次處理完，讓剩餘工程項有明確方向。不依賴裁決的純工程項已全部完成（#14、#22、#54、#57、#63、#65 及 #29／#31／#69 的可做部分，見 `STATUS.md`）。

## 優先行動

1. **裁決並處理 #2、#8、#11 的關閉爭議**：三者於 2026-10-01 依指示關閉，使用者其後表示有爭議、之後再討論。決定重開、維持關閉或改以新 Issue 承接殘留（#2 的 AC-1 無驗證紀錄；#8 的 OQ-12、#11 的 OQ-13 責任邊界未裁決）。
2. **裁決三個卡住實作的小決定**：[#31](https://github.com/bext1998/taylor-core/issues/31) 項次 4（相對路徑 `..` 逃逸是否在 spec §6.2 補說明；§6 為 `[FROZEN]`）、[#69](https://github.com/bext1998/taylor-core/issues/69) 第 3 項（`workspace_diff` 是否含 staged／untracked）、[#55](https://github.com/bext1998/taylor-core/issues/55) 的 `--report` 錯誤碼（目前 `E_REPORT_WRITE` 為暫定）。同時決定 [#62](https://github.com/bext1998/taylor-core/issues/62) 的 Pi runtime 錯誤碼名稱（spec 寫 `E_PI_RUNTIME_REQUIRED`、程式為 `E_RUNTIME_REQUIRED`）。
3. **決定 [#29](https://github.com/bext1998/taylor-core/issues/29) 是否關閉**：所有 AC、CI、PR 均有證據，INV-5 的 TOCTOU 殘餘限制已於 DECISIONS.md 2026-10-01 接受。

## 阻塞與待決策

- spec §16 的 OQ-3（`--report` 遇既有檔案的最終策略，目前暫行為 `E_FILE_EXISTS` 不覆寫）、OQ-12、OQ-13 尚未裁決；裁決前依各自的「裁決前行為」處理。
- 依 spec v1.3.4 仍開著、需要設計的後續：[#52](https://github.com/bext1998/taylor-core/issues/52)／[#53](https://github.com/bext1998/taylor-core/issues/53)（Host 保存紀錄並於 resume 交給新 Pi，不另建第二套 context 管理）、[#61](https://github.com/bext1998/taylor-core/issues/61)（Pi 升級前 Gate checklist）、[#72](https://github.com/bext1998/taylor-core/issues/72)（TUI 擴充，需先修訂凍結的 §4.2）。
- [#84](https://github.com/bext1998/taylor-core/issues/84)：`TestRunCancelAfterAppendFailure` 以固定 sleep 等待，慢 CI 偶發失敗；是不依賴裁決的小項，可隨時做。
- 無 Alpha 1 硬阻塞。

## 參考

- `STATUS.md`（本批次逐項結果與未驗證項）
- `DECISIONS.md`（2026-10-01 spec 過度工程化風險的裁決）
- `docs/spec.md` v1.3.4、`docs/adr/ADR-002-pi-agent-runtime.md`
- `MAZE_PROJECT.md`
