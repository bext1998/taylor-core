# Brunel — 當前狀態

> 最後同步：2026-10-02
> Branch：main
> 主線基準：`dab8be8`（PR #90 合併後；main CI 成功）

## 架構轉向

- [ADR-002](docs/adr/ADR-002-pi-agent-runtime.md)（2026-08-12）：放棄零依賴單檔 exe 需求（ADR-001 硬需求 (a)、spec.md 原 G-1），改採 Pi 作為 model-facing Agent Runtime（Route B）；ADR-001 部分 Superseded，Job Object／Workspace／Safety／PowerShell 執行器與 Go 1.25.x／Bubble Tea v2 基線仍維持 Go 實作。依據 [#24 Pi Compatibility Spike](https://github.com/bext1998/brunel/issues/24) 的 Gate 1/3/4 Pass、Gate 2 Pass（有但書）、Gate 0 Partial；Spike 分支 `agent/pi-spike-issue-24` 已 push 至遠端（不合併）。
- `docs/spec.md` 已同步修訂至 **v1.3**：8 個工具全留 Go（經 `taylor-tools.ts` extension 暴露給 Pi）、Session 以 Brunel 自己的 `events.jsonl` 為準（Pi session 停用）、Provider 開放多家（不再限定 OpenRouter，交由 Pi 生態決定）。新增 INV-9（`internal/pirpc` 禁止送出 `bash` RPC command）、OQ-8／OQ-9。
- 架構轉向已拆解為 Issues（2026-09-08）：#8（F-7）、#9（F-8）已依 v1.3 §4 矩陣改標題與範圍；新增 [#29](https://github.com/bext1998/brunel/issues/29)（`taylor-tools.ts` extension 與 `brunel.exe --taylor-tool` 派工，由 #9 拆出）、[#30](https://github.com/bext1998/brunel/issues/30)（INV-9 `bash` command 禁令與 CI lint／AST 檢查，由 #9 拆出），皆為 #1 的 sub-issue。#9 相依改為 Blocked by #4／#8／#29／#30。
- Gate 0（物理上無 Git Bash 的環境）補測依使用者裁決不另建 Issue，維持 ADR-002 現況：Git for Windows 為已文件化安裝依賴，spec OQ-9 視為接受風險、不驗證。OQ-8 的版本釘選／本機安裝／啟動版本檢查由 [#43](https://github.com/bext1998/taylor-core/issues/43) 與 PR #59 落地；升級前 Gate 等價測試 checklist 由 [#61](https://github.com/bext1998/taylor-core/issues/61) 追蹤。

## 進行中 Issues

- 2026-10-02 起 GitHub 上開著的 Issue 只剩 [#1](https://github.com/bext1998/brunel/issues/1)（Alpha 1 總追蹤）與 [#72](https://github.com/bext1998/taylor-core/issues/72)（TUI 擴充評估，需先修訂凍結的 §4.2，是否做由使用者決定）。下列 #69／#31／#29 與其餘追蹤項已關閉，條目保留供對照；Alpha 1 發布缺口見下方「Alpha 1 發布缺口」。
- [#69 承接 #4 的 minor 殘留](https://github.com/bext1998/taylor-core/issues/69) CLOSED：[PR #83](https://github.com/bext1998/taylor-core/pull/83)（`774f202`，`Related to #69`）已修正第 1、2、4、5、6、7、8、9、11 項（`workspace_diff` 取消／逾時與 stderr、`search_text` 上限、`apply_patch` null 行元素、`E_FILE_NOT_FOUND`、registry 測試）並逐項重驗留紀錄。第 3 項依 DECISIONS.md 2026-10-02 維持 `git diff -- <pathspec>` 並在程式註解記錄語意；第 10 項（本文件對 TC-SAFE 的措辭）已於 2026-10-02 同步修正。
- [#31 安全分類器強化](https://github.com/bext1998/taylor-core/issues/31) CLOSED：[PR #82](https://github.com/bext1998/taylor-core/pull/82)（`885e8ff`，`Related to #31`）完成項次 1～3（萬用字元刪除、逗號串接路徑、串接良性命令）；審查另抓出並修正引號內分隔字元、引號字串內插值、反引號跳脫引號三個降級風險。項次 4（相對路徑 `..` 逃逸不分類）依 DECISIONS.md 2026-10-02 記為 spec §16 OQ-14 的已知限制（PR #87，v1.3.5），不修改凍結的 §6；項次 5（可選）依 AGENTS.md 第 10 條不做。
- [#29 taylor-tools.ts 與 `--taylor-tool` 派工](https://github.com/bext1998/taylor-core/issues/29) CLOSED：[PR #78](https://github.com/bext1998/taylor-core/pull/78)（`d246008`，`Related to #29`）查證三項殘留——TS 測試 9 個全過、typebox 雙份安裝實測無衝突、INV-5 跨呼叫缺口確實存在並已修正（主程序把 session 綁定的 workspace identity 傳給每個 `--taylor-tool` 程序，不符回 `E_WORKSPACE_UNBOUND`）。DECISIONS.md 2026-10-01 已接受「檢查到實際 I/O 之間的 TOCTOU」為 Alpha 1 限制；舊式不帶 identity 的獨立呼叫沒有跨呼叫保證。「必要測試已通過」AC 已勾；已於 2026-10-01 關閉。
- 其餘追蹤項已完成並關閉：#61（PR #89，Pi 升級 Gate checklist）、#62（spec EC-13 對齊既有程式，PR #87）、#55（`E_REPORT_WRITE` 正式化 PR #87、寫入不可見半成品測試 PR #88；該測試驗證原子發布，不中斷寫入者，「寫入中取消」的傳遞與暫存檔清理沒有測試，維持為已知限制）、#52／#53（PR #90，Host 保存 session summary 並交給新 Pi，見下方「最近完成」）。
- [#1 Alpha 1：薄型 coding harness 實作追蹤](https://github.com/bext1998/brunel/issues/1) 已依 v1.2 對齊；2026-10-02 起 #14（PR #75）、#22（PR #77）、#54（PR #76）、#65（PR #74）、#57（PR #79）、#63（PR #80）已完成並關閉；#2、#8、#11 於 2026-10-01 依使用者指示關閉，但使用者其後有爭議，已於 2026-10-02 裁決維持關閉（見下方「已合併待關閉」）；#29、#31、#69 已於 2026-10-01 關閉，開著的只剩 #1 與 #72。（#4、#5、#7、#9 已關閉）（#30 已於 PR #36 完成並 CLOSED）。#8、#9 已依 ADR-002／v1.3 重新拆解完成（見「架構轉向」）；#4／#8／#9 核心實作已合併（見下）；#11 核心已合併（F-10 部分實作，見下），時序語意已由 #47 於 2026-09-18 裁決（採方向 1，#47 已關閉）。
- [#2 F-1：CLI、薄型 TUI 與 TTY 契約](https://github.com/bext1998/taylor-core/issues/2) 核心已透過 [PR #56](https://github.com/bext1998/taylor-core/pull/56) 合併至 `main`（merge commit `0c86a4c`，`Related to #2`）：`brunel`（TTY 啟動 TUI）、`brunel "<task>"`（純文字模式）、`--mode`／`--model`／`--name`／`--resume`／`--report`、退出碼 0／1／2（規格未定義數值，本 PR 自訂）；`internal/tui`（可捲動 transcript、多行輸入、狀態列、批准 modal）；`internal/approval`（每次執行專用的 Windows named pipe，把 `--taylor-tool` 子行程 Gate 的 CONFIRM 送回主程式；DACL 僅限目前使用者、拒絕遠端連線、每請求附 token、子行程讀取後移除環境變數、任何通道錯誤皆視為拒絕；見 DECISIONS.md 2026-09-28）；`go.mod` 升至 `go 1.25.0`（Bubble Tea v2.0.9、x/sys v0.47.0、x/term v0.45.0）。經多輪合併前審查修正：批准提示與純文字 TTY 輸出的終端控制字元（`SanitizeForDisplay`）、長命令批准 modal 需捲讀全文才能批准（含 End 跳頁與 resize 繞過）、TUI 取消後保留 aborted session、session 關閉失敗不回成功退出碼。**AC 尚不能全數打勾**：2026-10-01 已以真實 Pi 0.85.1 與真實終端機完成人工 TTY 操作（串流、resize、批准 modal、Ctrl+C）與 pipe 模式驗證（證據見 #2 留言與已關閉的 #49），結果無問題；人工驗證中發現的串流 panic（#67）已由 PR #68 修正。第 3 個驗收框的 AC-3（`--report` 的 JSON 完整）原依賴 #14，**#14 已由 PR #75 完成**（`modified_files`、`diff`、`verifications`、`tool_failures`、`pending_approval` 皆由 run 事實填入，並以真實 Pi 實跑確認）；#54 已由 PR #76 完成（無 TTY 遇需確認命令立即以 `E_APPROVAL_REQUIRED_NO_TTY` 非零結束）；`brunel login`／`logout` 由 PR #74 新增。AC-1 已於 spec v1.3.6 改為不要求乾淨 VM。**#2 於 2026-10-01 依使用者指示以 completed 關閉，2026-10-02 經 DECISIONS.md 裁決維持關閉。**已知未處理：子行程被取消後 TUI 上已開的批准 modal 不會自動關閉（無安全影響）；`taylor-tools.ts` 預設從 `brunel.exe` 同目錄載入，部署佈局屬 #43／#49。後續：#52（同一 session 多任務 context）、#53（`--resume` 重建 context）、#54（無 TTY 遇需確認命令時未立即以非零結束 run）、#55（`--report` 的 CT-8 路徑前置條件，銜接 #14）。
- [#4 F-3：8 個固定內建工具與凍結 schema](https://github.com/bext1998/brunel/issues/4) 的 `internal/tools` 核心實作已透過 PR #33 合併至 `main`：固定 registry、嚴格 JSON／必填欄位驗證（`DisallowUnknownFields`、拒 `null`／尾隨 JSON、無自動補值）、8 工具的結構化 `Result`，以及每個 I/O 路徑在 workspace resolve、`filetools`、Git 或 PowerShell 前經真實 `safety.Gate.Decide`（INV-1）。新增 `list_files`、`search_text`、Git-only `workspace_diff`；其餘工具接上既有 `filetools`／`exec`。經兩個隔離 Sonnet subagent 審查並修正兩個 major（INV-1 no-bypass 測試補齊 8 工具的 `Registry{Gate:nil}` 反例；`tools.ErrorCode` 串接 `safety`／`workspace`／`filetools`／`exec` 的 `ErrorCode`）。registry 層的測試涵蓋 INV-1 no-bypass（8 工具）、readonly 拒絕、批准被拒無副作用與錯誤碼串接，Windows AC-6 閉環測試通過（TC-SAFE 的分類細節測在 `internal/safety`，registry 層沒有逐類 CONFIRM 命令的覆蓋）；CI `windows-latest`＋`ubuntu-latest` 綠。殘留 minor／nit（`workspace_diff` timeout 混碼與漏 staged／untracked、`search_text` 無上限、`max_depth:0` 未文件化、巢狀 null coerce 等）記於 [#4 review 註解](https://github.com/bext1998/brunel/issues/4#issuecomment-5620453317)。`--taylor-tool` 進入點與 `taylor-tools.ts` 已由 PR #37（#29 核心）合併；Pi bridge 已由 PR #40（#9 核心）合併，見下方 #9 條目。 **2026-10-01 已以 completed 關閉**：8 個固定工具皆已在真實 Pi 下成功呼叫（`create_file`／`write_file` 補驗見 #4 留言，雜湊已獨立核對），10 項 minor 改由 [#69](https://github.com/bext1998/taylor-core/issues/69) 承接。
- [#5 F-4：實作 stale-read hash 防護與原子寫入](https://github.com/bext1998/brunel/issues/5) 核心實作已透過 PR #26 合併至 `main`（`internal/filetools`）：全檔 SHA-256、`create_file`／`write_file`／`apply_patch` 的 `expected_hash` 前置條件、精確 hunk 套用（無自動 merge／模糊比對）、暫存檔＋鎖內重驗＋原子換檔；stale hash、patch conflict、overlap、缺失目標、失敗寫入與鎖衝突皆保留原檔且有界失敗。#4（PR #33）已將它接至 `workspace.Workspace` 與 §5.4 工具呼叫路徑，並經 PR #37 的 `brunel --taylor-tool` 進入點可從子行程呼叫；#9 核心（PR #40）已合併。2026-10-01 真實 `pi --mode rpc` 下 `apply_patch`、`create_file`、`write_file` 皆成功，`write_file` 帶錯誤雜湊回 `E_STALE_HASH` 且檔案不變；#5 已於 2026-09-18 關閉。INV-6 為 best-effort（見 spec §10／OQ-10）。
- [#7 F-6：實作 AUTO／CONFIRM 事故防護與 Approver](https://github.com/bext1998/brunel/issues/7) 核心實作已透過 PR #27 合併至 `main`（`internal/safety`）：單一安全決策入口 `Gate.Decide`、`Risk`／`ApprovalPrompt`／`Approver`（依 spec.md §5.2／§6.2 FROZEN 定義）、readonly 模式的確定性拒絕（不呼叫 Approver）、無 TTY 時 `E_APPROVAL_REQUIRED_NO_TTY` 快速失敗、`run_powershell` 針對 §6.2 六類代表命令（強制／遞迴刪除、清空內容、大量移動覆寫、git 狀態變更、安裝或更新套件、網路傳輸、背景程序／job、workspace 外絕對路徑）的字串／token 分類。#4（PR #33）已在 8 工具的實際 I/O 前接上 `Gate.Decide`、`filetools`、`workspace` 與 `exec`（INV-1 反例測試已涵蓋 8 工具）；實際 TUI／純文字 Approver 仍屬 #2。best-effort classifier 的漏判／過度確認強化見 [#31](https://github.com/bext1998/brunel/issues/31)。 2026-10-01 真實 Pi 下已驗證：唯讀模式拒絕、無 TTY 不執行需確認命令、TUI 中 6 類 CONFIRM 命令皆顯示完整命令與理由且拒絕無副作用、批准不被記憶、一般任務不跳視窗（證據見 #7 留言；第一個驗收框已據此勾選）。無 TTY 時退出碼仍為 0，屬 #54。#7 已於 2026-09-18 關閉。
- [#8 F-7：實作 Provider Adapter（Pi delegated，ADR-002）](https://github.com/bext1998/brunel/issues/8) 核心實作已透過 PR #28 合併至 `main`（`internal/pirpc` 新套件＋共用的 `internal/redact` leaf 套件）：`BuildArgs` 依 spec §5.1 字面凍結命令組出啟動參數；`LaunchOptions.EffectiveProvider()` 是唯一的 effective-provider 解析點（顯式 `Provider`，否則取 `Model` 第一段前綴），`InjectCredentialsForLaunch` 用它注入憑證，因此只給 provider-prefixed model（不帶 `--provider`）也拿得到 Credential Manager key；`InjectCredentials` 要求傳入 `Credential{Provider, APIKey}`，provider 身份不符回 `E_PI_CREDENTIAL_MISMATCH` 且不注入，既有變數以 `EqualFold` 比對（Windows 大小寫不敏感）並改寫為正式名稱。`TranslateProviderError` 先以**原始**訊息分類，再只把遮罩後訊息放進公開 error（協定錯誤碼沿用 spec EC-11 的 `E_PROVIDER_PROTOCOL`）；`internal/redact.Secrets` 除啟發式規則外可接受呼叫端已知的實際 credential 值做精確替換（涵蓋 `sk-` 以外格式）。**殘留限制**：Pi 自行探索、Brunel 從未持有的 provider key 只剩啟發式遮罩，公開錯誤契約在該邊界的責任歸屬仍待 spec 決議。INV-9 的完整 AST 檢查＋CI 階段與正式 `TC-PIRPC-001` 已由 PR #36（Issue #30，CLOSED）完成（`go/ast` 解析、`+` 串接／同檔 const 解析、`json.Unmarshal` 後檢查 `type=="bash"`；`go test` 前的 CI step）。Pi 子行程啟動／生命週期管理與 RPC event 轉譯已由 PR #40（#9 核心）合併。 2026-10-01：[PR #70](https://github.com/bext1998/taylor-core/pull/70)（`22011a8`）補上 Pi 拒絕 prompt 時 `error` 文字的解碼與轉譯（沒有 key → `E_PI_PROVIDER_AUTH`），以及 OpenRouter「not a valid model」→ `E_PI_MODEL_NOT_FOUND`；真實 Pi 重跑確認。#8 仍卡在 OQ-12（provider 自行回顯部分遮罩 key 的責任邊界）。**#8 於 2026-10-01 依使用者指示關閉（OQ-12 明寫「不阻塞其餘 #8 範圍關閉」，關閉不代表責任邊界已裁決）；使用者其後有爭議，已於 2026-10-02 裁決維持關閉。**
- [#11 F-10：AGENTS.md 就近目錄規則載入與不可提權保護](https://github.com/bext1998/brunel/issues/11) 核心已透過 [PR #46](https://github.com/bext1998/taylor-core/pull/46) 合併至 `main`（**F-10 部分實作**）：`taylorToolResponse`（派工層 wrapper，非 FROZEN `tools.Result`）新增可選 `agents_md` 欄位；`nearAgentsMD` 由目標目錄往上走到 workspace root（不含 root）、每層經 `workspace.Workspace.Resolve()` 解析、候選檔以 `os.Lstat` 判定（symlink／非常規檔案跳過）、較近者優先、無快取；成功呼叫的結果由 `taylor-tools.ts` 附加一個 `agents_md` text 區塊送達模型；AGENTS.md 內容不進入 `Gate.Decide`／分類／hash guard 的任何輸入（不可提權，AC-5 行為測試覆蓋：聲稱免確認不降級 CONFIRM、聲稱忽略 hash 不繞過 `E_STALE_HASH`）。經 Codex 合併前審查兩輪：(1) blocker——候選檔原以 `os.Stat`（跟隨 symlink）可讀 workspace 外檔案，改 `os.Lstat` 並加 2 個回歸測試（`1f80b19`）；(2) major——spec §7.3「操作**前**按需讀取」在 `--mode rpc` 無 call 前 context 注入通道下無法完全達成，實際語意為規則隨**觸及該目錄的第一個 tool result**送達（首次操作在模型看到規則前完成），此語意已由 [#47](https://github.com/bext1998/taylor-core/issues/47) 於 2026-09-18 裁決接受（採方向 1，見 DECISIONS.md，#47 已關閉）。殘留 check-then-read 微秒 TOCTOU 與既有 `filetools`／`workspace` 模式相同（OQ-10 已接受此類競態），複核認定對齊既有標準。2026-10-01 已在真實 Pi 下確認就近規則隨 tool result 送達（只對 `calc/calc.go` 呼叫一次 `read_file`，模型即原文取得 `calc/AGENTS.md` 的規則）；OQ-13 四個邊界案例仍待使用者裁決。**#11 於 2026-10-01 依使用者指示關閉（OQ-13 接受四項為 Alpha 1 已知限制，不要求逐項修復）；使用者其後有爭議，已於 2026-10-02 裁決維持關閉。**
- [#22 F-13：建立 Alpha 1 三類 E2E fixtures](https://github.com/bext1998/brunel/issues/22) 已新增；#2、#7、#9、#14 已分別同步薄型 TUI、事故防護、EventSink 與客觀 CompletionReport 範圍。**2026-10-02 已由 [PR #77](https://github.com/bext1998/taylor-core/pull/77) 完成並關閉**：`e2e/` 三個小型 Go fixture（bug 修復、小功能、失敗測試診斷），預設測試驗證修正前失敗／參考解答通過／來源 hash 不變；選擇性的 Windows 測試用標準 CLI 對真實模型實跑（`openrouter/anthropic/claude-haiku-4.5` 三個皆過，約 92 秒），守衛檢查所有 `*_test.go`、`go.mod`、`go.sum` 未被改、刪或新增。只用一個模型各跑一次，不當預設 CI 閘門。

## 阻塞 Issues

- 無規格決策阻塞 Alpha 1 實作。`docs/spec.md` §5／§9 的 Route B 修訂已於 v1.3（`7e9e01e`）完成。
- #13（完成證據狀態機）與 #15（Smoke Benchmark Runner）已依 v1.2 以 `not planned` 關閉。
- 2026-10-02：`docs/spec.md` 已升至 **v1.3.4**（PR #81，依 `docs/todo.md` 與 DECISIONS.md 2026-10-01 的使用者裁決）：保留 resume 恢復承諾並明定 Host／Pi context 分工、接受 INV-5 的檢查至 I/O TOCTOU 為 Alpha 1 限制、允許維持行為與公開格式的內部 Go 型別重構、區分變更驗證與發布驗證；OQ-12／OQ-13 不因此解決。
- 2026-10-02 待裁決批次已由 DECISIONS.md 2026-10-02 處理：#8、#11 維持關閉；#31 項次 4 記為 OQ-14；#69 第 3 項維持現況；#55 正式化 `E_REPORT_WRITE`；#62 以 spec 對齊程式。**#2 維持關閉**：DECISIONS.md 2026-10-02 取代先前「重開 #2」的決定；AC-1 隨後於 spec v1.3.6 改為不要求乾淨 VM，已無待驗項目。「無開放阻塞」只指沒有阻擋開工的設計決策，發布缺口見下方「Alpha 1 發布缺口」。

## Alpha 1 發布缺口

發布門檻是 AC-1～AC-16 全部通過（spec §12，發布驗收見 §13）。依 AGENTS.md 第 10 條，驗證範圍只到各 AC 與已知風險，不另加驗證。

- **只有 AC-14 的證據缺口保留、本次不補**（見下）；其餘無待補驗收項目。AC-1 已依使用者裁決在 spec v1.3.6 改為「在符合已聲明依賴的 Windows 開發機啟動 exe」，Node.js/npm、pwsh、Git 為已聲明前提（Pi 本身就需要它們），不另測缺依賴情境，也不要求乾淨 VM。
- **AC-2～AC-16 皆有證據（AC-14 為部分涵蓋，見下）**：AC-2、AC-10 有 2026-10-01 的人工 TTY 驗證；AC-3～AC-6、AC-9、AC-11 有真實 Pi 驗證；AC-7、AC-8、AC-12、AC-13、AC-14、AC-15 由自動化測試涵蓋（`internal/filetools`、`internal/workspace`、`internal/exec`、`internal/session`、`internal/completion`、`internal/agent`）；AC-16 三個 fixture 以 `claude-haiku-4.5` 各跑一次皆過。**AC-14 只部分涵蓋**：有摘要後從 log 重載與 append-only 的測試，但沒有「摘要前後原始 bytes 不變」的比對；依使用者指示不另補、不另開 Issue，發布時如實記為此限制。
- **已接受的限制（不阻塞）**：OQ-12（Pi 自行探索的 key 被回顯時只有啟發式遮罩）、OQ-13（就近 AGENTS.md 的 4 個邊界案例）、OQ-14（相對路徑逃逸不分類）、OQ-10（INV-6 best-effort）。TUI 第二任務路徑未用真實 TUI 驗證，與 `--resume` 共用機制、靠測試涵蓋。
- **仍未裁決**：OQ-1（Windows 最低支援版本；發布聲明不超出實測版本，只實測過這台開發機）、OQ-3（`--report` 既有檔案，暫行不覆寫）。

## 等待 Review

- 無。

## 等待 Merge

- 無。

## 已合併待關閉

- **#2、#8、#11 的關閉有爭議**：（2026-10-02 已由 DECISIONS.md 裁決：#8、#11 維持關閉、#2 後續改為維持關閉，見上方）三者於 2026-10-01 依使用者指示以 completed 關閉（#8 的 OQ-12、#11 的 OQ-13 責任邊界／邊界案例未裁決，各 Issue 的關閉留言已如實寫明）；使用者其後有爭議，已於 2026-10-02 裁決三者皆維持關閉（歷史紀錄）。
- 已關閉（供對照）：#4（2026-10-01）、#5／#7／#9（2026-09-18）、#49／#67（2026-10-01）；2026-10-01～02 另關閉 #14、#22、#29、#31、#52、#53、#54、#55、#57、#61、#62、#63、#65、#69、#84；#66 併入 #65 後以重複關閉。

## 最近完成

- 2026-10-02 後半批次（皆 squash 合併，main CI 成功）：
  - [PR #87](https://github.com/bext1998/taylor-core/pull/87)（#31、#55、#62、#69、#2）：spec 升至 v1.3.5，新增 OQ-14、列出 `--report` 失敗碼、EC-13 對齊 `E_RUNTIME_REQUIRED` 並補 `E_PI_VERSION_MISMATCH`；`workspace_diff` 只加註解。
  - [PR #88](https://github.com/bext1998/taylor-core/pull/88)（#55）：測試「report 永遠不會以寫到一半的樣子被看見」。
  - [PR #89](https://github.com/bext1998/taylor-core/pull/89)（#61）：Pi 升級前 Gate 等價測試 checklist。
  - [PR #90](https://github.com/bext1998/taylor-core/pull/90)（#52、#53）：每個任務結束時 Host 依觀察到的事實存 `summary.json`，下個任務／`--resume` 把它放進給新 Pi 的 prompt（DECISIONS.md 2026-10-02）。不恢復批准，只記錄被拒絕的命令；資料缺漏、過時或事件紀錄尾端破損時 prompt 明說「不知道」；先遮罩再截斷、summary 自己記錄寫入失敗。Codex 審查三輪，三項修正做過 mutation 檢查。真實 Pi：`--resume` 後模型不用工具就答對先前建的檔名與內容（測試 session 已刪）。**限制**：模型得到的是摘要事實、不含上次最終回答文字；TUI 路徑沒有用真實 TUI 驗證，與 resume 共用同一機制、靠測試涵蓋；提示文字不構成完整的 prompt-injection 防護。
- 2026-10-01～02 工程批次（皆 squash 合併；審查與 CI 逐項如下，不以「都已審查／都通過」概括）：#74～#80、#82、#83、#86 的審查結果由 Codex（#80 由 Pi）留言於各 PR，且合併前的 PR CI 通過（#83 的 Windows 第一次 run 失敗於無關的 agent 測試，重跑通過）；**#73 與 #81 沒有 PR 審查留言，#81 最後一次 PR CI run（head `6d14ce1`）的 Windows 為失敗**（合併後 main 上 `a2c0b62` 的 CI 為成功）。**main 在 #82、#83 合併後的 push CI 曾失敗**（`885e8ff`、`774f202`），原因是 #75 讓 `Run` 一開始先跑 `git status`，使 `internal/agent` 兩個以固定 50ms sleep 等待的取消測試在負載下取消過早；由 PR #86（#84）修正，#86 合併後 main（`dfdd603`）的 CI 為成功：
  - [PR #73](https://github.com/bext1998/taylor-core/pull/73)：`AGENTS.md` 新增第 10 條「不得 over-engineering」。
  - [PR #74](https://github.com/bext1998/taylor-core/pull/74)（#65）：Credential Manager blob 先當 UTF-8、否則當 UTF-16LE 解碼，無法解讀以 `E_CONFIG_CREDENTIAL` 拒絕並提示；含 NUL 或非法 UTF-8 的 key 不得進入 Pi 環境；`CreateProcess` 失敗訊息帶 Win32 原因；新增 `brunel login`／`logout`（隱藏輸入或 stdin 第一行，不接受參數）。以暫時替換並還原真 key 的方式實測 `cmdkey` UTF-16 key、`login`／`logout`、真終端機隱藏輸入與 Ctrl+C，並以空的 `PI_CODING_AGENT_DIR` 證明該 key 確實被送到 OpenRouter（401）。
  - [PR #75](https://github.com/bext1998/taylor-core/pull/75)（#14）：CompletionReport 由工具事實填入；`completed` 在有未終態呼叫、待批准或批准被拒時降為 `incomplete`；`--report` 在 run 前檢查（workspace 內、父目錄存在、不覆寫，OQ-3 暫行規則 `E_FILE_EXISTS`），寫入以 hard link 原子發佈。
  - [PR #76](https://github.com/bext1998/taylor-core/pull/76)（#54）：無 TTY 遇需確認命令立即中止 Pi、報告 `incomplete` 帶 `pending_approval`、以 `E_APPROVAL_REQUIRED_NO_TTY` 非零結束。
  - [PR #77](https://github.com/bext1998/taylor-core/pull/77)（#22）：AC-16 三類 E2E fixtures，見上方 #22 條目。
  - [PR #78](https://github.com/bext1998/taylor-core/pull/78)（#29 殘留）：INV-5 跨呼叫 workspace identity，見上方 #29 條目。
  - [PR #79](https://github.com/bext1998/taylor-core/pull/79)（#57）：`TestPSRunner_Timeout_KillsProcessTree` 的 2 秒 timeout 在孫程序（第二個 pwsh 冷啟動，閒置約 0.85～0.97 秒）未啟動時會誤判；加壓重現後改為孫程序從未啟動的嘗試以 2s→5s→12s 重試，kill 斷言不變。
  - [PR #80](https://github.com/bext1998/taylor-core/pull/80)（#63）：Pi 版本探測改在 `KILL_ON_JOB_CLOSE` Job Object 內執行，任何結束方式皆終止整個 job；新測試以握著 stdout 的孫程序驗證三種結束方式，換回舊實作會失敗。
  - [PR #81](https://github.com/bext1998/taylor-core/pull/81)：spec v1.3.4，見「阻塞 Issues」。
  - [PR #82](https://github.com/bext1998/taylor-core/pull/82)（#31 項次 1～3）、[PR #83](https://github.com/bext1998/taylor-core/pull/83)（#69），見上方「進行中 Issues」。
  - [PR #86](https://github.com/bext1998/taylor-core/pull/86)（#84）：取消測試改為等 run 處理過第一個 text delta 再取消，斷言不變；24 個空轉 pwsh 負載下原測試連跑 15 次失敗 20 個斷言、修正後全過。
- 同一批次新開 [#72](https://github.com/bext1998/taylor-core/issues/72)（TUI 擴充評估）；[#84](https://github.com/bext1998/taylor-core/issues/84) 已由 PR #86 關閉。

- 2026-10-01 真實 Pi 驗證與收尾：#49 已以 completed 關閉（AC-6、AC-9、#8 透傳／注入、#11 就近規則送達皆通過）。8 個固定工具先前只跑過 6 個，`create_file`、`write_file` 於後續補驗成功（雜湊已獨立核對）。#4 於同日以 completed 關閉；#5／#7／#9 早於 2026-09-18 關閉，本文件先前仍把它們列為待關閉，已更正。
- [PR #68](https://github.com/bext1998/taylor-core/pull/68)（#67，`Related to #67`）已 squash 合併（`ff518a1`）：互動 TUI 串流回覆第二個片段起 panic（`model.assistant` 為 `strings.Builder`，Bubble Tea 每則訊息複製 model）改為 `string`，新增 `TestStreamingSurvivesModelCopies`。由 codex 審查後合併；#67 已關閉。
- [PR #70](https://github.com/bext1998/taylor-core/pull/70)（#8，`Related to #8`）已 squash 合併（`22011a8`）：見上方 #8 條目。由 codex 審查（APPROVE、無 finding），Windows／Ubuntu CI 通過；#8 未自動關閉。
- 本輪新開的 Issue：[#65](https://github.com/bext1998/taylor-core/issues/65)（OpenRouter key 存取：`cmdkey` 存的 UTF-16 被當 UTF-8 讀出 NUL，造成 `E_RUNTIME_REQUIRED` 且錯誤看不出原因，並補上有文件的設定方式；已併入原 #66）、[#69](https://github.com/bext1998/taylor-core/issues/69)（承接 #4 的 10 項 minor，外加 `create_file` 上層資料夾不存在時錯誤訊息籠統的新觀察）。#66 以重複關閉。
- [#43 Pi 版本釘選與本機安裝](https://github.com/bext1998/taylor-core/issues/43)：[PR #59](https://github.com/bext1998/taylor-core/pull/59) 已合併（`26c3112`，`Related to #43`）。Pi `0.85.1` 改為一般 dependency、同步 lockfile；`Start` 優先讀取 extension／Brunel 安裝目錄內套件 manifest 的 `bin.pi`，以 Node 直接啟動，缺本機套件才 fallback PATH，損壞則明確失敗。每次 RPC 啟動前嚴格檢查版本，版本不符回 `E_PI_VERSION_MISMATCH`；部署必要步驟與目錄配置已寫入 README。四項程式驗收均有測試／CI 證據；文件 PR #60 已合併（`3b0fa7d`）；2026-09-30 依使用者裁決以 completed 關閉。正式升級 checklist、錯誤碼對齊、版本探測程序樹生命週期分別由 #61／#62／#63 承接，完整模型／工具 E2E 仍由 #49 追蹤。
- [PR #59](https://github.com/bext1998/taylor-core/pull/59)（#43，`Related to #43`）已於 2026-09-30 squash 合併至 `main`（`26c3112`）。[最後一輪 CI](https://github.com/bext1998/taylor-core/actions/runs/36673223324) Windows target／Ubuntu portability guard 均通過；Windows `TestStartLocalInstalledPi` 實際執行並回應 RPC `get_state`，包含不同版本全域 shim 的本機優先驗證，`TestPiInvocationGlobalNpmShim`／版本不符拒絕測試／`TestTCPIRPC001` 皆通過。Windows build／vet／全套 test、零 CGO build、npm ci／test、本機 Linux amd64 交叉編譯與 vet 通過；使用者另驗證 Linux build／vet／test 及 Windows 交叉 vet。lockfile 僅移除 peer 標記與相應逗號，既有套件版本／integrity 不變。審查中的 STATUS／NEXT_ACTION 變更已在合併前全部撤銷，本次於 PR 合併後另行同步。
- PR #56（#2 F-1 核心，**Related to #2**）已合併至 `main`（merge commit `0c86a4c`，5 個提交）：見上方 #2 條目。經多輪隔離審查後合併，最後一輪 Windows／Ubuntu CI 皆綠。審查過程中 `d4ba3ce` 的 Windows CI 曾在 `internal/exec` 的 `TestPSRunner_Timeout_KillsProcessTree` 失敗（該目錄未變更，後續提交未重跑即通過），已另開 [#57](https://github.com/bext1998/taylor-core/issues/57) 追蹤。
- PR #51（忽略 `node_modules/`、加入 `package-lock.json`）由使用者裁決 CLOSED（未合併）：`main` 已有 `package-lock.json`（#45）與 `node_modules/` 忽略規則，該 PR 與 `main` 衝突且會以較舊內容覆蓋；Pi 版本鎖定與本機安裝改由 #43 追蹤。
- PR #46（#11 F-10 核心，**部分實作**，`Related to #11`）已合併至 `main`（merge commit `9842177`，commits `cee4d92`＋`1f80b19`）：見上方 #11 條目。經 Codex 合併前審查兩輪（symlink blocker 修復＋時序轉 #47）。
- PR #40（#9 F-8 核心）已合併至 `main`（squash，commit `c620351`）：見上方 #9 條目。
- PR #36（#30 F-x INV-9，`Closes #30`）已合併至 `main`：`internal/pirpc` 的 `bash` RPC command 禁令從過渡子字串 tripwire 升級為 `go/ast` 靜態檢查（解析字串字面／`+` 串接／同檔 const，`json.Unmarshal` 後查 `type=="bash"`，回報 `file:line`），`TestTCPIRPC001` 正反例，CI 於 `go test` 前執行。#30 已 CLOSED。
- PR #37（#29 核心，`Related to #29`）已合併至 `main`：新增 repo 第一個可執行檔 `cmd/brunel`（最小 `--taylor-tool` 進入點）＋ `taylor-tools.ts`／`package.json`／`tsconfig.json`。經 Opus 隔離審查（changes-requested）後修正三個 major：`BRUNEL_MODE` 改 fail-safe（未設預設 `readonly`）、`catch` 區塊不再把非 JSON 失敗吞成 `SyntaxError`、`maxBuffer` 4→64 MiB。三個隔離審查（Haiku／Sonnet／Opus）另見各 PR 討論。
- PR #38（`AGENTS.md` 工作原則第 7 條）已合併至 `main`：Git worktree 集中放 `D:\AgentCoding\.codex\worktrees\Brunel`，分支名 `maze/YYYY-MM-DD-<隨機hash>` 無字尾。
- PR #33（#4 F-3，`internal/tools`）已合併至 `main`：8 工具固定 registry、嚴格參數驗證、結構化結果、INV-1 前置裁決接線；經兩個隔離 Sonnet subagent 審查後修正兩個 major（INV-1 反例補齊 8 工具、`ErrorCode` 串接依賴碼）。殘留 minor 記於 #4 review 註解，並交叉引用 #29／#9。
- PR #34（CI ubuntu 列標籤）已合併至 `main`：matrix 改 `include` 形式加 `role`（`target`／`portability-guard`），標頭註解說明 ubuntu 列僅為可攜性／分層防線、非支援目標。check 名稱改為 `build-test (<os> - <role>)`。
- PR #32（`CLAUDE.md` 匯入 `AGENTS.md`）已合併至 `main`：讓 Claude Code 與其他 agent 共用同一份入庫指令。
- PR #28（#8 F-7 Provider Adapter，`internal/pirpc`＋`internal/redact`）已合併至 `main`；經兩輪 review 修正（provider 錯誤訊息遮罩、協定錯誤碼對齊 spec EC-11 `E_PROVIDER_PROTOCOL`、`Credential` provider 綁定與 `E_PI_CREDENTIAL_MISMATCH`、`EffectiveProvider` 前綴解析、先分類再遮罩、`redact` 已知值精確遮罩、Windows 環境變數大小寫不敏感取代、INV-9 tripwire 誠實化）。INV-9 完整 AST/CI 防線仍屬 #30。
- PR #26（#5 F-4 stale-read，`internal/filetools`）與 PR #27（#7 F-6 AUTO／CONFIRM，`internal/safety`）已合併至 `main`；均經多輪 review 修正（#26：非 Windows no-overwrite install、有界非阻塞鎖、誠實化 INV-6；#27：分隔符旁路、路徑正規化、覆寫／大量移動命令形式）。CI 於 `windows-latest` + `ubuntu-latest` 矩陣通過。
- `docs/spec.md` 提升為 **v1.3.1**：§10 INV-6 標註為 best-effort、新增 §16 OQ-10（檔案寫入的 sub-millisecond rename 競態，Alpha 1 接受為 best-effort，Alpha 3「單一 writer」時重評）。
- ADR-002 架構轉向拆解為 Issues（`maze-spec-to-issues`）：#8／#9 改標題與範圍、新增 #29／#30（皆由 #9 拆出、掛在 #1 下、assignee `bext1998`）；Gate 0 補測依使用者裁決不另建、CAND-1（OQ-8）暫緩；無新標籤。
- [ADR-002](docs/adr/ADR-002-pi-agent-runtime.md) 確認：完成 Issue #24 Pi Compatibility Spike（5 Gate，證據存於已 push 的 `agent/pi-spike-issue-24` 分支）並據此裁決放棄零依賴需求、轉向 Route B。
- PR #23（v1.2 規格與相關文件對齊）已合併至 `main`。
- 完成 v1.2 GitHub Issue 同步：更新 #1、#2、#4、#5、#7、#8、#9、#11、#14，關閉 #13／#15，新增 #22；候選 F15～F17 未建立。
- 完成並取得使用者裁決的 Alpha 1 v1.2 規格：安全收斂為事故防護、加入薄型 TUI、簡化完成報告並將 benchmark runner 移回 Alpha 4。
- #6（F-5 PowerShell Job Object 執行器）已透過 PR #20 合併至 `main` 並關閉；經三輪 review 修正 pipe read handle 重複關閉、`TerminateJobObject` 錯誤處理與有界等待（含 pipe drain）、handle 繼承 mutex 範圍不足。
- #3（F-2 Workspace）已透過 PR #19 合併至 `main` 並關閉（root 真實路徑／identity 綁定、junction／絕對路徑／symlink 逃逸攔截與 TC-WS 測試全數通過）。
- #10／#12 已分別透過 PR #16／#17 合併至 `main` 並關閉。
- 完成 Alpha 1 v1.1 規格補強；已由 v1.2 取代。
- 建立 Git、GitHub 與 Maze 專案治理基礎。
- 依 `docs/spec.md` 需求追蹤矩陣建立 #1～#15、結構化標籤與原生父子關係。

## 未追蹤本機工作

- PR #21（GitHub Actions CI workflow，`.github/workflows/ci.yml`）已合併至 `main`，無對應 Issue；每次 push／PR 自動跑 `go build`／`go vet`／`go test`／零 CGO build，矩陣 `windows-latest`（target）＋`ubuntu-latest`（portability-guard，PR #34 標籤化）。
- PR #32（`CLAUDE.md` → `@AGENTS.md`）、PR #34（CI 矩陣標籤與註解）、PR #35／PR #38（`STATUS.md`／`NEXT_ACTION.md` 同步、`AGENTS.md` 規則 7）已合併至 `main`，皆無對應 Issue（專案治理輔助）。

## 已知驗證限制

- 真實 Pi 驗證範圍（2026-10-01，Pi 0.85.1、`openrouter/z-ai/glm-5.3-prime`，證據見已關閉的 #49 與各 Issue 留言）：pipe 模式完整閉環（8 個工具皆被呼叫成功）、AUTO 0 次批准、provider／model 透傳與 Credential Manager 憑證注入、就近 AGENTS.md 隨 tool result 送達、唯讀與無 TTY 的拒絕、互動 TUI 的人工操作（串流、resize、批准 modal、Ctrl+C）皆已驗證。**未涵蓋**：AC-14（context 摘要後重載）、AC-7 的「讀取後外部改檔」真實競爭情境（只驗了錯誤雜湊 → `E_STALE_HASH`）、AC-1、長命令批准的必讀到底流程、provider 額度類錯誤。
- #59 審查非阻塞事項：spec EC-13 的 `E_PI_RUNTIME_REQUIRED` 與既有程式 `E_RUNTIME_REQUIRED` 不一致，新增 `E_PI_VERSION_MISMATCH` 尚未納入 spec；版本探測繼承父行程環境，但不接收 RPC 啟動的額外憑證／批准 token 注入。版本探測的 Job Object 缺口已由 PR #80（#63）補上；錯誤碼對齊仍由 [#62](https://github.com/bext1998/taylor-core/issues/62) 追蹤。此處只記錄事實，未改動 spec 或裁決 INV-7 例外。
- v1.2 AC-7 的 stale-read 防護（#5）：核心與單元測試已合併（PR #26），經 #4／PR #37／PR #40 接上完整路徑；真實 Pi 下 `write_file` 帶錯誤雜湊回 `E_STALE_HASH`，檔案前後 SHA-256 相同、無暫存檔殘留（2026-10-01）。尚未做「讀取後外部改檔再寫入」的真實競爭情境。
- INV-6 為 best-effort（spec v1.3.1／OQ-10）：`internal/filetools` 無法完全消除鎖內重驗與原子換檔之間的 sub-millisecond rename 競態；Alpha 1 接受（併發寫入者僅外部人為編輯，Brunel 內部無併發 writer），Alpha 3「單一 writer」時重評。
- AC-9～AC-11：真實 Pi 下已觀察到 pipe 模式完整閉環 0 次批准詢問（AC-9）；TUI 中 6 類 CONFIRM 命令皆顯示完整命令與理由、拒絕無副作用、同一命令批准兩次仍會再問、一般任務不跳視窗（AC-10）；唯讀模式寫入與 `run_powershell` 回 `E_READONLY_MODE`（AC-11）。無 TTY 時退出碼為 0 且模型會繼續嘗試後續命令的缺口已由 PR #76（#54）修正，並以真實 Pi（stdin 非 TTY、要求 `git push origin main`）確認退出碼 1、stderr 含 `E_APPROVAL_REQUIRED_NO_TTY`、後續命令不再執行。§6.2 classifier 的漏判／過度確認強化見 #31。
- AC-4（Provider 與憑證）：#8 的透傳、憑證注入與錯誤轉譯已在真實 Pi 驗證；PR #70（2026-10-01）補上 Pi 拒絕 prompt 時的 `error` 文字解碼（沒有 key → `E_PI_PROVIDER_AUTH`）與 OpenRouter 不存在 model id → `E_PI_MODEL_NOT_FOUND`。Pi 自行探索、Brunel 從未持有的 provider key 若被回顯於錯誤訊息，只能做啟發式遮罩：例如 OpenAI 會回顯部分遮罩的 key（前綴加末 4 碼）並原樣通過，完整 key 未被回顯。公開錯誤不含 secret 的最終責任邊界仍待 spec 定義（OQ-12，#8 的未勾驗收框卡在這裡）。Pi 的原文會原樣帶出，包含 `Use /login` 等對 Brunel 無意義的提示。
- PR #41（原 STATUS/NEXT_ACTION 同步文件，內容已過期）由使用者主動 CLOSED（未合併），本輪同步取代其內容。
- `internal/exec` 的 Timeout／MaxProcesses／MaxMemoryBytes／MaxOutputBytes 一律由呼叫端明確提供，套件本身不內建預設值；Alpha 1 不再需要 benchmark 硬性預算。
- `go.mod` 已於 PR #56 升至 `go 1.25.0`；新增或升級依賴時須確認其 `go` 指令不高於 1.25（`AGENTS.md` 已載明）。
- PR #56 的批准通道與 TUI：2026-10-01 已由作者在真實終端機人工驗證（串流、resize、批准 modal 的 `y`／`n`、Ctrl+C 取消、離開後無殘留 Brunel／Pi 程序）；驗證中發現的串流 panic（`strings.Builder` 被值複製，#67）已由 PR #68 修正。離開後的殘留程序檢查是事後以命令列判斷，沒有在執行中做「有→無」對照。

- 2026-10-01～02 批次的未驗證項：`pending_approval`：無 TTY 的情境已由 PR #76 以真實 Pi 驗證（報告 `incomplete` 並帶該欄位），但「TTY 批准中繼器提供 `pending_approval`」的情境（批准提示開著時被取消）未實測，只有單元測試；TUI 路徑（PR #76 之後）沒有用真實 TUI 驗證；E2E fixtures 只對單一模型各跑一次；INV-5 的 `--workspace-id` 在舊式獨立呼叫端缺席時不檢查；PR #82 的分類器仍為 best-effort（項次 4 相對路徑 `..` 逃逸不分類）。