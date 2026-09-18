# Brunel — 決策紀錄

> 格式：每條決策包含「時間、決策內容、原因、影響範圍」；最新決策置頂。

## 決策紀錄

### 2026-09-18 — Issue #47：收尾三項後續事項（spec 文字、compaction 落差、去重 key 設計）

**決策**：延續同日稍早「方向 1」裁決（見下一則），收尾當時列出的三項未決事項：

1. **`docs/spec.md` §7.3／AC-5 文字**：已落地（v1.3.2）。§7.3 改寫為「root 於啟動注入、子目錄規則於首次觸及該目錄的成功工具結果送達，自該次起對同目錄後續操作生效，首次操作不受此層約束」；AC-5 通過標準同步改寫為「首次操作後、同目錄後續操作時就近規則生效，且不得授權工具」。
2. **Context compaction 擠掉已送達規則的落差**：不在 Alpha 1 修，記為 §16 OQ-11。`internal/pirpc` 現況不轉譯 `compaction_start`/`compaction_end` event（#9 範圍外），`taylor-tools.ts` 也拿不到此訊號可用於重置 per-session 已送集合。修法需先讓 Pi RPC 把 compaction 事件曝露給 `taylor-tools.ts`，屬另開 issue 的範圍，不卡 F-10／Issue #47 驗收。
3. **`agentsMDKey` 用「目錄＋內容」而非純目錄路徑去重**：維持現況，不改。`agents-md-delivery.ts` 現有實作與行內註解已說明理由——Go 端不快取、每次呼叫都重讀檔案，若只用目錄當 key，規則檔案中途被編輯時模型會抱著送達當下的舊版本一路用到 session 結束；用「目錄＋內容」當 key 則規則一改就會重送最新版本，用「多送一次」換「不會讓模型用過期規則工作」，符合 F-10 原始目的（讓模型看到就近規則）。

**原因**：三項事項性質不同，分開裁決：#1 是已有裁決文字的落地執行，直接照抄；#2 屬於「context-only 規則的傳遞完整性」問題，不影響 AC-5 安全不變式（Go 端授權不讀這個訊號），且修法成本卡在 Pi RPC 尚未曝露的事件，比照原裁決「不切 Alpha 1」的一貫立場；#3 是在覆核 `agents-md-delivery.ts` 既有設計後確認該設計本身已經是對的——避免模型抱過期副本的風險，高於「同一規則在同一 session 內可能被送兩次」的重複成本，沒有理由為了單純的去重簡化而犧牲正確性。

**影響範圍**：`docs/spec.md`（§7.3、AC-5、§16 OQ-11、版本升到 v1.3.2、§18 修訂記錄）；`agents-md-delivery.ts`（確認維持現況，無程式碼變更）；GitHub Issue [#47](https://github.com/bext1998/taylor-core/issues/47)。

**狀態**：確認

---

### 2026-09-18 — Issue #47：接受「操作後送達」為 F-10 就近 AGENTS.md 的 Alpha 1 語意

**決策**：Issue #47 三個候選方向中採**方向 1**——把現行「就近 AGENTS.md 附在觸及該目錄的第一個成功工具結果」裁決為 Alpha 1 可接受的 F-10 語意，不採方向 2（新 RPC context 注入指令）、不採方向 3（兩段式工具呼叫、延後 I/O）。後續需要：（a）改寫 `docs/spec.md` §7.3 為兩句——root AGENTS.md 於啟動注入，子目錄規則於首次觸及該目錄的成功工具結果送達、自該次之後生效，首次操作不受此層約束；（b）AC-5 驗收方式同步改寫為「首次操作後、同目錄後續操作時就近規則生效，且不得授權工具」；（c）`taylor-tools.ts` 加 per-session 已送集合，讓「一次送達」成為實作保證而非每次重送；（d）root prompt 補一句低成本緩解：進入未知規則的目錄時先 `list_files` 探勘；（e）以下邊界案例記入 spec 或 Open Questions 追蹤——首次操作若為 `write_file`/`create_file`/`apply_patch` 屬不可逆寫入、規則事後才到擋不住已發生的寫入；`run_powershell` 只按 cwd 查規則，指令內部再 cd 時目標目錄的規則不會送達；AGENTS.md 為 symlink 時被靜默略過（1f80b19），模型無從得知「此處有規則但被略過」；工具呼叫失敗時規則不送達，模型修正後重試仍未見規則。方向 2（Pi RPC context 注入通道）視為未來可另開 issue 向 Pi 協定提案的獨立項目，不卡在 F-10 驗收前提。

**原因**：透過 herdr 讓 Claude、Codex（gpt-5.6-sol）、Pi（deepseek-v4.1）三個 agent 各自讀過 `cmd/brunel/main.go` 的 `nearAgentsMD`、`taylor-tools.ts` 注入點與 `docs/spec.md` §7.3／AC-5 後獨立提出建議，結論收斂為：agents_md 只在 `Registry.Call` 成功後才產生、只讀、只餵進 response（`cmd/brunel/main.go` 約 183-202 行），全程走 `Workspace.Resolve` + `Lstat`，不進 gate、分類或 hash guard，因此現況**沒有違反 AC-5「不能授權工具」的安全不變式**，落差只在「就近規則生效」的時序讀法與 spec §7.3 字面「操作前」不符。方向 3 會改變已 `[FROZEN]` 的工具契約語意（一次呼叫不再等於一次操作），牽動 §9 前置條件與 AC-6 八工具閉環；且因規則是 context-only、不改變 Go 端授權結果，「先拿到規則再執行」不會讓任何原本會通過的操作變成被擋，只換到延遲與重試/pending 狀態管理的複雜度，安全收益是假的。方向 2 的完成綁在 Brunel 控制不了的上游 Pi 協定升級（版本相容、handshake），不該當作 F-10 驗收前提。Codex 的意見（傾向方向 3，若不承擔成本則退回方向 1）與 Pi 的意見（直接主張方向 1）在「不違反 AC-5 安全不變式」上一致，僅在是否值得為時序字面吻合付出 FROZEN 契約變更成本上分歧；本決策採 Pi 論證中「context-only 規則不影響 Go 端授權」這一點作為裁決依據。

**影響範圍**：`docs/spec.md` §7.3、AC-5（文字修訂為後續 PR，本決策僅記錄裁決方向，尚未落地）；`taylor-tools.ts`（per-session 已送集合，尚未落地）；GitHub Issue [#47](https://github.com/bext1998/taylor-core/issues/47)（裁決依據，已留言摘要）、Issue #11（相關）、PR #46（原始實作與已知限制的說明來源）。若日後推動方向 2，需另開新 Issue 向 Pi 協定提案。

**狀態**：確認

---

### 2026-09-08 — 接受檔案寫入的殘餘 rename 競態為 Alpha 1 best-effort

**決策**：#5（PR #26，`internal/filetools`）的 stale-read 防護採「`expected_hash` 前置條件 + 鎖內重驗 hash + 暫存檔原子換檔」。此設計無法完全消除「外部程序在鎖內重驗與換檔之間以 rename 蓋掉目標檔」的 sub-millisecond 競態（OS byte-range lock 不擋 rename、POSIX flock 為 advisory）。Alpha 1 接受此殘餘競態為 best-effort，不再投入完整修法。`docs/spec.md` §10 INV-6 標註為 best-effort，新增 §16 OQ-10，規格提升為 v1.3.1。

**原因**：命中條件嚴苛（需外部人為編輯剛好落在微秒級視窗），blast radius 僅為單次未提交併發編輯遺失、非檔案損毀、非累積、git 可救；未破壞任何 `[FROZEN]` 契約，AC-7 仍通過。完整修法（改 in-place rewrite）會失去寫入的 crash 原子性，得不償失；整個生態（git、編輯器、同類 agent 工具）都容忍此等級殘餘。Alpha 1 定位本就是「事故防護、非 sandbox」。

**影響範圍**：`docs/spec.md` §10 INV-6／§16 OQ-10／§18；`internal/filetools`（程式碼註解已誠實揭露）。Alpha 3「單一 writer」時重評——屆時若 Brunel 內部出現併發 writer，需加 path-keyed 序列化層。

**狀態**：確認

---

### 2026-09-08 — ADR-002 後續拆解為 Issues；Gate 0 補測不另建、視為接受風險

**決策**：將 ADR-002「後續需要」拆解為 GitHub Issues。(2)(3) 已落地：#8（F-7）／#9（F-8）依 v1.3 §4 矩陣改標題與範圍，並由 #9 拆出 [#29](https://github.com/bext1998/brunel/issues/29)（`taylor-tools.ts` extension 與 `brunel.exe --taylor-tool` 派工）與 [#30](https://github.com/bext1998/brunel/issues/30)（INV-9 `bash` command 禁令與 CI lint／AST 檢查），皆為 #1 sub-issue、assignee `bext1998`。(1)「未安裝 Git Bash 的環境補測 Gate 0」**不另建 Issue**，維持 ADR-002 現況：Git for Windows 為與 `pwsh` 7 同級的已文件化安裝依賴，spec.md OQ-9 視為接受風險、不做驗證。OQ-8（Pi 版本釘選政策）暫緩，尚未建 Issue。

**原因**：`docs/spec.md` §5／§9 的 Route B 修訂已在 v1.3（`7e9e01e`）完成，ADR-002 後續 (2)(3) 的規格前提已具備，可直接拆 Issue。Gate 0 的「物理上無 bash」情境目前沒有任何 AC／EC 依賴（AC-1 已假設 Git for Windows 已安裝、EC-13 針對缺 Node），把它當成獨立發布門檻 Issue 過重；多數目標環境本來就會有 Git Bash，維持文件化依賴即可。

**影響範圍**：`STATUS.md`、`NEXT_ACTION.md`、GitHub Issues #1／#8／#9／#29／#30。可執行前線為 #7、#8、#30（#5 已在 PR #26 review 中）。

**狀態**：確認

---

### 2026-08-12 — 放棄零依賴單檔 exe 需求，採用 Pi 作為 Model-facing Agent Runtime（Route B）

**決策**：放棄 ADR-001 硬需求 (a)「乾淨 Windows x64 環境下載單一 `brunel.exe` 即可執行，不需預裝任何 runtime」（spec.md G-1 現行文字為「無需預裝 Go 或 Node.js」，兩者皆與本決策衝突）。改採 [ADR-002](docs/adr/ADR-002-pi-agent-runtime.md)：以 [earendil-works/pi](https://github.com/earendil-works/pi) 作為 model-facing Agent Runtime（Provider abstraction、Agent Loop、Tool-call lifecycle），透過 RPC 模式被 Go Host 呼叫；Go 繼續持有 Workspace boundary、Safety 決策模型、PowerShell 7 執行器與 Windows Job Object 這幾項 Host-only authority。ADR-001 標記為部分 Superseded，硬需求 (b)（Job Object）、Go 1.25.x／Bubble Tea v2 工具鏈基線與程序控制部分維持有效。

**原因**：Issue #24 的 5-Gate Spike（分支 `agent/pi-spike-issue-24`，已 push 至遠端、未合併，可拋棄原型）證實 Gate 1（Model Tool Authority）、Gate 3（Windows Process Authority）、Gate 4（Dual Runtime Complexity）皆 Pass，Gate 2（RPC Side Channel）Pass 但需 Taylor RPC client 自建 `bash` command 禁止清單；Gate 0（Windows Runtime Requirement）因測試機已裝 Git Bash 只完成 Partial。使用者評估後認為「零依賴單檔 exe」在現實中本來就難以完全達成（Brunel 本身已要求 `pwsh` 7 另外安裝），繼續以此為硬約束不合理；放棄該需求後，原本選擇 Go native 而非 Node/Pi 的兩個理由之一（零依賴）不再成立，繼續自建多供應商 Agent Loop 的長期維護負擔不再有相抵收益。

**影響範圍**：`docs/adr/ADR-001-runtime-language.md`（狀態部分 Superseded）、新增 `docs/adr/ADR-002-pi-agent-runtime.md`、`docs/spec.md` G-1 與修訂記錄（標註待正式修訂，§5／§9 等架構章節留待 Route B 整合設計完成後再修）、`STATUS.md`、`NEXT_ACTION.md`。直接受影響的既有 Issue：#8（F-7 Provider Adapter）、#9（F-8 Agent Loop/EventSink/context）需重新檢視是否仍以 Go 自建。後續需要：(1) 在未安裝 Git Bash 的 Windows VM/runner 補測 Gate 0；(2) 設計並實作 Taylor RPC client 的 `bash` command allowlist/lint 防線；(3) 將 Route B 的正式整合工作拆解為 GitHub Issues。

**狀態**：確認

---

### 2026-07-14 — Alpha 1 v1.2 收斂安全、TUI、完成報告與 benchmark 邊界

**決策**：安全定位改為事故防護與 `AUTO`／`CONFIRM`；Alpha 1 採 Go 1.25.x + Bubble Tea v2 薄型 TUI；CompletionReport 只記客觀事實；benchmark runner 移回 Alpha 4；`docs/spec.md` 合併重複契約為 v1.2 單一來源。

**原因**：原安全模型嘗試精細分類任意 PowerShell，複雜度高但無法提供相稱保證；完成證據狀態機只能檢查模型填寫的字串；benchmark runner 與產品提案的 Phase 邊界衝突。薄型 TUI 則改善互動透明度，但必須與 agent core 解耦。

**影響範圍**：`docs/spec.md`、Go 工具鏈 ADR、Alpha 1 Issue 範圍、安全與 completion 介面、CLI／TUI 架構及測試計畫。

**狀態**：確認

---

### 2026-07-13 — 建立公開 GitHub repository 與 Maze 工作流

**決策**：以 `bext1998/brunel` 作為公開 repository；使用 GitHub Issues 與 spec-to-issues，採 `priority: P1`、`type: bug` 結構化標籤，預設指派 `bext1998`，並允許建立缺少的標籤。Coding Agent 使用 Codex 與 Claude Code。

**原因**：讓 Alpha 1 的需求、驗收條件與實作進度可追蹤，並提供跨 Coding Agent 的一致專案定位。

**影響範圍**：GitHub repository 設定、`MAZE_PROJECT.md`、`AGENTS.md`、狀態與後續 Issue 工作流。

**狀態**：確認

---

<!-- 新決策按時間順序追加於最上方。規格內既有決策以 docs/spec.md 修訂記錄為準，不在此重複宣告。 -->
