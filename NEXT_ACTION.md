# Brunel — 下一步行動

> 最後同步：2026-10-02

## 下一個 Session 目標

Alpha 1 工程項已全數合併，AC-1 已於 spec v1.3.6 改為不要求乾淨 VM，除 AC-14 證據缺口（見下）外沒有待補的驗收項目。GitHub 上開著的只有 [#1](https://github.com/bext1998/brunel/issues/1)（總追蹤）與 [#72](https://github.com/bext1998/taylor-core/issues/72)（TUI 擴充評估）。

## 優先行動

1. 發布前依 spec §13 彙整 AC-1～AC-16 證據（`STATUS.md`「Alpha 1 發布缺口」已列出），再依使用者決定處理 #1 的關閉與發布。
2. AC-14 的證據缺口保留、本次不補（缺「摘要前後原始 bytes 不變」比對）。spec §12 要求 AC-1～AC-16 全部通過，因此發布時須如實記載此缺口，或由使用者裁決放寬 AC-14，不能寫成已滿足。

## 待決策

- **#72**：要不要做、做什麼範圍，由使用者決定；需先修訂凍結的 §4.2。
- spec §16：OQ-1（Windows 最低支援版本，發布聲明不超出實測版本）、OQ-3 未裁決；OQ-12／OQ-13／OQ-14 已接受為 Alpha 1 限制。

## 參考

- `STATUS.md`、`DECISIONS.md`（2026-10-02 兩則裁決）
- `docs/spec.md` v1.3.6 §12～§13、`docs/adr/ADR-002-pi-agent-runtime.md`
- `MAZE_PROJECT.md`
