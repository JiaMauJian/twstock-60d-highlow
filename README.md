# 台股創 X 日新高新低比例

抓取台股上市＋上櫃個股歷史股價，計算每日「創 N 日（預設 60 日）新高／新低」的家數與比例，
輸出成 Excel，並可自動發布成 GitHub Pages 網頁。

## 執行方式

### 本機互動模式
```bash
go run .
```
會依序詢問是否清空 `data/` 快取，跑完產生 `股價創60日新高新低比例.xlsx` 與 `web/data.json`。

### 非互動模式（供排程／CI）
```bash
go run . -auto          # 使用現有 data/ 快取，不重抓
go run . -auto -clear   # 先清空 data/ 再全部重抓
```

## 網頁版（GitHub Actions + GitHub Pages）

架構：GitHub Actions 每個交易日收盤後自動執行爬蟲 → 產生 `web/data.json` → 部署到 GitHub Pages，
任何人開網址即可看走勢圖。全程免費、不需自備伺服器。

```
[排程] .github/workflows/update.yml (cron 每日 17:30 台灣時間)
   └ go run . -auto → web/data.json
[部署] GitHub Pages ← 上傳 web/ 資料夾
[前端] web/index.html 讀 data.json，用 Chart.js 畫圖
```

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

之後每個交易日會自動更新，也可隨時在 Actions 頁面手動觸發。

### 注意事項

- **Excel 自動產生**：`股價創60日新高新低比例.xlsx` 由程式自動建立（不存在就新建、存在就沿用），
  已列入 `.gitignore`，不需提交進 repo。
- **首次 CI 較久**：第一次沒有 `data/` 快取，會逐檔下載上千檔（每檔間隔 300ms，約數分鐘）。
  之後透過 `actions/cache` 保存快取，僅補抓新資料，會快很多。
- **資料來源**：個股與大盤來自 Yahoo Finance，上市櫃清單來自臺灣證券交易所（isin.twse.com.tw）。

## 計算邏輯重點

- 「創 N 日新低」：某日收盤 ≤ 前 N 日（含當日）所有收盤 → 記為 1，否則 0。新高同理取最大。
- 個股與大盤的每日結果以**日期字串對齊**，個股當天若停牌／無資料則該格留空，
  不計入當日「家數」分母，確保比例準確。
- 上市未滿 N 日的個股，早期不足視窗的日子不計算（正常行為）。
