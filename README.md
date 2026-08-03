# 台股創 X 日新高新低比例

抓取台股上市＋上櫃個股歷史股價，計算每日「創 N 日（預設 60 日）新高／新低」的家數與比例，
輸出成 `web/data.json`，並可自動發布成 GitHub Pages 網頁。

## 執行方式

### 本機互動模式
```bash
go run .
```
會依序詢問是否清空 `data/` 快取，跑完產生 `web/data.json`。

### 非互動模式（供排程／CI）
```bash
go run . -auto          # 使用現有 data/ 快取，不重抓
go run . -auto -clear   # 先清空 data/ 再全部重抓
```

## 網頁版（GitHub Actions + GitHub Pages）

架構：外部排程（cron-job.org）每個交易日收盤後呼叫 GitHub API 觸發 workflow →
執行爬蟲產生 `web/data.json` → 部署到 GitHub Pages，任何人開網址即可看走勢圖。全程免費、不需自備伺服器。

```
[排程] cron-job.org (每日 17:30 台灣時間) ──POST──▶ GitHub API (workflow_dispatch)
[執行] .github/workflows/update.yml → go run . -auto → web/data.json
[部署] GitHub Pages ← 上傳 web/ 資料夾
[前端] web/index.html 讀 data.json，用 Chart.js 畫圖
```

> 不用 GitHub 內建的 `schedule` cron，因為它在尖峰時段常延遲甚至漏跑；改用外部排程主動觸發較可靠。

### 首次上線步驟

1. **建立 GitHub repo 並推送**（在專案資料夾內）：
   ```bash
   git init
   git add .
   git commit -m "初版：新增網頁版與自動更新"
   git branch -M main
   git remote add origin https://github.com/<你的帳號>/<repo名稱>.git
   git push -u origin main
   ```

2. **開啟 GitHub Pages**：進入 repo 的 **Settings → Pages**，
   將 **Source** 設為 **GitHub Actions**。

3. **手動觸發一次**：進入 **Actions** 分頁 → 選「更新股價新高新低資料並部署」→ **Run workflow**。
   跑完後，Pages 網址（`https://<帳號>.github.io/<repo名稱>/`）即可看到圖表。

### 設定 cron-job.org 自動排程

用 [cron-job.org](https://console.cron-job.org/) 每個交易日定時呼叫 GitHub API 觸發 workflow：

1. **建立 GitHub Token**：GitHub → **Settings → Developer settings → Personal access tokens → Fine-grained tokens**。
   - Repository access：只選這個 repo
   - Permissions → **Actions: Read and write**
   - 產生後複製 token（只會顯示一次）

2. **在 cron-job.org 新增 Cronjob**：
   - **URL**：`https://api.github.com/repos/JiaMauJian/twstock-60d-highlow/actions/workflows/update.yml/dispatches`
   - **Request method**：`POST`
   - **Headers**：
     ```
     Accept: application/vnd.github+json
     Authorization: Bearer <你的_TOKEN>
     X-GitHub-Api-Version: 2022-11-28
     ```
   - **Request body**：`{"ref":"main"}`
   - **Schedule**：每週一～五 17:30（台灣時間）

   成功時 GitHub API 回應 **204 No Content**（無內容即代表已觸發）。

之後即由 cron-job.org 定時觸發，也可隨時在 Actions 頁面手動 Run workflow。

### 注意事項

- **輸出**：程式只輸出 `web/data.json`（給網頁讀）。已移除舊版的 Excel 輸出。
- **首次 CI 較久**：第一次沒有 `data/` 快取，會逐檔下載上千檔（每檔間隔 300ms，約數分鐘）。
  之後透過 `actions/cache` 保存快取，僅補抓新資料，會快很多。
- **資料來源**：個股與大盤來自 Yahoo Finance，上市櫃清單來自臺灣證券交易所（isin.twse.com.tw）。

## 計算邏輯重點

- 「創 N 日新低」：某日收盤 ≤ 前 N 日（含當日）所有收盤 → 記為 1，否則 0。新高同理取最大。
- 個股與大盤的每日結果以**日期字串對齊**，個股當天若停牌／無資料則該格留空，
  不計入當日「家數」分母，確保比例準確。
- 上市未滿 N 日的個股，早期不足視窗的日子不計算（正常行為）。
