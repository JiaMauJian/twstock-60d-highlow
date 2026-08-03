package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// https://mholt.github.io/json-to-go/
type Stock struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency             string  `json:"currency"`
				Symbol               string  `json:"symbol"`
				ExchangeName         string  `json:"exchangeName"`
				InstrumentType       string  `json:"instrumentType"`
				FirstTradeDate       int     `json:"firstTradeDate"`
				RegularMarketTime    int     `json:"regularMarketTime"`
				Gmtoffset            int     `json:"gmtoffset"`
				Timezone             string  `json:"timezone"`
				ExchangeTimezoneName string  `json:"exchangeTimezoneName"`
				RegularMarketPrice   float64 `json:"regularMarketPrice"`
				ChartPreviousClose   float64 `json:"chartPreviousClose"`
				PriceHint            int     `json:"priceHint"`
				CurrentTradingPeriod struct {
					Pre struct {
						Timezone  string `json:"timezone"`
						End       int    `json:"end"`
						Start     int    `json:"start"`
						Gmtoffset int    `json:"gmtoffset"`
					} `json:"pre"`
					Regular struct {
						Timezone  string `json:"timezone"`
						End       int    `json:"end"`
						Start     int    `json:"start"`
						Gmtoffset int    `json:"gmtoffset"`
					} `json:"regular"`
					Post struct {
						Timezone  string `json:"timezone"`
						End       int    `json:"end"`
						Start     int    `json:"start"`
						Gmtoffset int    `json:"gmtoffset"`
					} `json:"post"`
				} `json:"currentTradingPeriod"`
				DataGranularity string   `json:"dataGranularity"`
				Range           string   `json:"range"`
				ValidRanges     []string `json:"validRanges"`
			} `json:"meta"`
			Timestamp  []int `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Volume []interface{} `json:"volume"`
					Low    []interface{} `json:"low"`
					Open   []interface{} `json:"open"`
					High   []interface{} `json:"high"`
					Close  []interface{} `json:"close"`
				} `json:"quote"`
				Adjclose []struct {
					Adjclose []float64 `json:"adjclose"`
				} `json:"adjclose"`
			} `json:"indicators"`
		} `json:"result"`
		Error interface{} `json:"error"`
	} `json:"chart"`
}

type Data struct {
	OptionNum    string
	IsNewLows    map[string]int // 日期 -> 是否創新低(1/0)，該股當天沒交易則不存在此 key
	IsNewHighs   map[string]int // 日期 -> 是否創新高(1/0)，該股當天沒交易則不存在此 key
	IsSharpDrops map[string]int // 日期 -> 當日相對前一交易日收盤是否跌幅 >= 9%(接近跌停)(1/0)
	Url          string
}

type StockInfo struct {
	Code string
	Type string
}

// MarketDay 是輸出給前端網頁的每日一筆統計
type MarketDay struct {
	Date      string  `json:"date"`
	Close     float64 `json:"close"`     // 大盤收盤
	LowCount  int     `json:"lowCount"`  // 當日創X日新低家數
	HighCount int     `json:"highCount"` // 當日創X日新高家數
	Total     int     `json:"total"`     // 當日有交易的家數(比例分母)
	LowPct    float64 `json:"lowPct"`    // 創新低比例(%)
	HighPct   float64 `json:"highPct"`   // 創新高比例(%)
	DropCount int     `json:"dropCount"` // 當日單日跌幅 >= 9%(接近跌停) 家數
	DropPct   float64 `json:"dropPct"`   // 當日單日跌幅 >= 9%(接近跌停) 比例(%)
	Valid     bool    `json:"valid"`     // 個股資料是否正常；false 表示當天各項統計應留空白
}

// WebOutput 是 web/data.json 的完整結構
type WebOutput struct {
	DayParameter int         `json:"dayParameter"`
	Generated    string      `json:"generated"`
	Days         []MarketDay `json:"days"`
}

var dataRange string = "20y" // 1y 5y 10y 20y

var dayParameter int = 60

func main() {

	autoMode := flag.Bool("auto", false, "非互動模式，供 GitHub Actions 等排程使用(不詢問、不等待輸入)")
	clearData := flag.Bool("clear", false, "啟動時清空 data 資料夾(僅在 -auto 模式下生效)")
	flag.Parse()

	var deleteData string
	if *autoMode {
		// 非互動模式：不詢問，由 -clear 旗標決定是否清空
		if *clearData {
			deleteData = "y"
		} else {
			deleteData = "n"
		}
	} else {
		for {
			fmt.Println("是否清空data資料夾(y/n)：")
			fmt.Scanln(&deleteData)

			deleteData = strings.ToLower(deleteData)

			if deleteData == "y" || deleteData == "n" {
				break
			} else {
				fmt.Println("輸入錯誤，請重新輸入 (y 或 n)")
			}
		}
	}

	var marketChoice int
	marketChoice = 1

	// 俊澐有需要的時候在打開
	// for {
	// 	fmt.Println("請選擇大盤市場：")
	// 	fmt.Println("1. 上市")
	// 	fmt.Println("2. 上櫃")
	// 	fmt.Scanln(&marketChoice)

	// 	if marketChoice == 1 || marketChoice == 2 {
	// 		break
	// 	} else {
	// 		fmt.Println("輸入錯誤，請重新輸入 (1 或 2)")
	// 	}
	// }

	if deleteData == "y" {
		os.RemoveAll("data/")
		os.MkdirAll("data/", 0600)
	}

	// 下載公司清單
	stockInfos, err := DownloadStockList()
	if err != nil {
		panic(err)
	}

	size := len(stockInfos)

	//https://www.yahoofinanceapi.com/pricing
	// 限制：每分鐘300個request
	fmt.Printf("下載股價中...\n")
	var filterStockInfos []StockInfo
	for i, stockInfo := range stockInfos {
		if DownloadData(stockInfo) {
			fmt.Printf("\r(%d/%d)", i+1, size)

			// download成功的加入filterStockInfos中，後面要跑迴圈計算最高最低數值
			filterStockInfos = append(filterStockInfos, stockInfo)
		}
	}

	dataFs, err := ioutil.ReadDir("data")
	if err != nil {
		panic(err)
	}
	fmt.Printf("\n已下載家數: %d\n\n", len(dataFs))

	defer func() {
		if *autoMode {
			fmt.Println("完成")
		} else {
			fmt.Println("完成，請按Enter鍵離開")
			fmt.Scanln()
		}
	}()

	var markets = map[int]string{
		1: "https://query1.finance.yahoo.com/v8/finance/chart/^twii",
		2: "https://query1.finance.yahoo.com/v8/finance/chart/^twoii",
	}
	marketUrl := markets[marketChoice]

	// 抓大盤資料
	tse, err := fetchMarketData(marketUrl, dataRange)
	if err != nil {
		fmt.Println(err)
		return
	}

	// 找出沒開盤的日子
	var nilCloseDates []string
	tseTimestamp := tse.Chart.Result[0].Timestamp
	for i, t := range tseTimestamp {
		date := time.Unix(int64(t), 0).Format("2006/01/02")
		closeValue := tse.Chart.Result[0].Indicators.Quote[0].Close[i]
		if closeValue == nil {
			nilCloseDates = append(nilCloseDates, date)
		}
	}

	fmt.Println("沒開盤日期", nilCloseDates)

	// 偵測 Yahoo 資料異常日：大盤有值、但幾乎所有個股收盤為 null，
	// 造成分母只有個位數的假數據。這類日子「不排除」（仍留在表頭），
	// 但後面會把新高/新低留空白，讓人看得出當天資料有問題。
	participation := buildParticipation(filterStockInfos)
	var marketDates []string
	for _, t := range tseTimestamp {
		date := time.Unix(int64(t), 0).Format("2006/01/02")
		if contains(nilCloseDates, date) {
			continue
		}
		marketDates = append(marketDates, date)
	}
	badDates := findLowParticipationDates(marketDates, participation)
	fmt.Println("個股參與度異常日(新高新低留空白):", badDates)

	fmt.Printf("最高最低計算中...\n")
	allRes := []Data{}
	for i, stockInfo := range filterStockInfos {
		DoCalc(stockInfo, &allRes, nilCloseDates)
		fmt.Printf("\r(%d/%d)", i+1, size)
	}

	// 依日期在記憶體中聚合各股的新高/新低結果
	// sumInXDayLow/High = 當日創新低/新高家數；sumInXDayHighCount = 當日有計算值的家數(比例分母)
	fmt.Println("依日期統計新高新低家數...")
	sumInXDayLow := map[string]int{}
	sumInXDayHigh := map[string]int{}
	sumInXDayHighCount := map[string]int{}
	sumSharpDrop := map[string]int{} // 當日單日跌幅 >= 9%(接近跌停) 家數
	for _, d := range allRes {
		for date, v := range d.IsNewLows {
			sumInXDayLow[date] += v
		}
		for date, v := range d.IsNewHighs {
			sumInXDayHigh[date] += v
			sumInXDayHighCount[date]++
		}
		for date, v := range d.IsSharpDrops {
			sumSharpDrop[date] += v
		}
	}

	// 依時間順序(舊→新)組出每日統計，輸出給前端
	fmt.Println("整理每日統計...")
	var webDays []MarketDay
	for i, t := range tseTimestamp {
		date := time.Unix(int64(t), 0).Format("2006/01/02")
		if contains(nilCloseDates, date) { // 大盤沒開盤的日子不輸出
			continue
		}

		closeVal := 0.0
		if cv := tse.Chart.Result[0].Indicators.Quote[0].Close[i]; cv != nil {
			closeVal = cv.(float64)
		}

		// 資料異常日：新高/新低留空白(Valid=false)，只保留大盤收盤
		if contains(badDates, date) {
			webDays = append(webDays, MarketDay{Date: date, Close: closeVal, Valid: false})
			continue
		}

		total := sumInXDayHighCount[date]
		var lowP, highP, dropP float64
		if total > 0 {
			lowP = float64(sumInXDayLow[date]) / float64(total) * 100
			highP = float64(sumInXDayHigh[date]) / float64(total) * 100
			dropP = float64(sumSharpDrop[date]) / float64(total) * 100
		}
		webDays = append(webDays, MarketDay{
			Date:      date,
			Close:     closeVal,
			LowCount:  sumInXDayLow[date],
			HighCount: sumInXDayHigh[date],
			Total:     total,
			LowPct:    lowP,
			HighPct:   highP,
			DropCount: sumSharpDrop[date],
			DropPct:   dropP,
			Valid:     true,
		})
	}

	// 輸出前端網頁用的 JSON
	writeWebJSON(webDays)
}

// writeWebJSON 把每日統計輸出成 web/data.json，供 GitHub Pages 前端讀取
func writeWebJSON(days []MarketDay) {
	if err := os.MkdirAll("web", 0755); err != nil {
		fmt.Printf("建立 web 資料夾失敗: %v\n", err)
		return
	}
	out := WebOutput{
		DayParameter: dayParameter,
		Generated:    time.Now().Format("2006/01/02 15:04:05"),
		Days:         days,
	}
	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		fmt.Printf("產生 JSON 失敗: %v\n", err)
		return
	}
	if err := os.WriteFile("web/data.json", b, 0644); err != nil {
		fmt.Printf("寫入 web/data.json 失敗: %v\n", err)
		return
	}
	fmt.Printf("已輸出 web/data.json（共 %d 筆）\n", len(days))
}

func fetchMarketData(marketUrl, dataRange string) (*Stock, error) {
	req, err := http.NewRequest("GET", marketUrl+"?range="+dataRange+"&interval=1d", nil)
	if err != nil {
		return nil, fmt.Errorf("建立請求失敗: %v", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("請求失敗: %v", err)
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("讀取回應失敗: %v", err)
	}

	var tse Stock
	if err := json.Unmarshal(body, &tse); err != nil {
		return nil, fmt.Errorf("解析回應失敗: %v", err)
	}

	return &tse, nil
}

func DownloadData(stockInfo StockInfo) bool {

	notFound := false
	_, err := os.Stat("data/" + stockInfo.Code)
	if err != nil {
		notFound = true
	}

	// 如果沒有檔案就去下載，並寫入檔案
	if notFound {
		time.Sleep(time.Millisecond * 300)

		url := "https://query1.finance.yahoo.com/v8/finance/chart/" + stockInfo.Code + ".TW?range=" + dataRange + "&interval=1d&lang=zh"
		if stockInfo.Type == "4" {
			url = "https://query1.finance.yahoo.com/v8/finance/chart/" + stockInfo.Code + ".TWO?range=" + dataRange + "&interval=1d&lang=zh"
		}

		req, err := http.NewRequest("GET", url, nil)
		if err != nil {
			fmt.Printf("股號: %s 忽略計算，請求建立失敗: %v，股票網址: %s\n", stockInfo.Code, err, url)
			return false
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36")

		client := &http.Client{}
		resp, err := client.Do(req)
		if err != nil {
			fmt.Printf("股號: %s 忽略計算，請求失敗: %v，股票網址: %s\n", stockInfo.Code, err, url)
			return false
		}
		defer resp.Body.Close()

		if resp.StatusCode != 200 {
			fmt.Printf("股號: %s 忽略計算，回應狀態碼: %v，股票網址: %s\n", stockInfo.Code, resp.StatusCode, resp.Request.URL)
			return false
		}

		body, _ := ioutil.ReadAll(resp.Body)

		// 寫入檔案
		f, err := os.OpenFile("data/"+stockInfo.Code, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		f.Write(body)
	}

	return true
}

func DoCalc(stockInfo StockInfo, allRes *[]Data, nilCloseDates []string) {

	body, err := ioutil.ReadFile("data/" + stockInfo.Code)
	if err != nil {
		panic(err)
	}

	var stock Stock
	err = json.Unmarshal(body, &stock)
	if err != nil {
		fmt.Printf("json解析失敗，股票檔案: %s", stockInfo.Code)
		return
	}

	// 檔案下載失敗，砍掉
	if len(stock.Chart.Result) == 0 {
		fmt.Println("刪除下載失敗的檔案 " + stockInfo.Code)
		err := os.Remove("data/" + stockInfo.Code)
		if err != nil {
			panic(err)
		}
		return
	}

	// 筆數
	timestamp := stock.Chart.Result[0].Timestamp
	close := stock.Chart.Result[0].Indicators.Quote[0].Close
	closeLen := len(close) - 1
	shift := 0
	window := dayParameter
	isNewLows := map[string]int{}
	isSharpDrops := map[string]int{}
	// 開始計算股價低於X日的結果
	for {
		e := closeLen - shift
		s := e - window
		shift++

		if s <= 0 {
			break
		}

		// close[e] 為 nil 代表該股當天沒交易，跳過不計(Excel 留空)，不填 0
		if close[e] == nil {
			continue
		}

		currentDate := time.Unix(int64(timestamp[e]), 0).Format("2006/01/02")
		if contains(nilCloseDates, currentDate) { //大盤沒開盤的日子不在表頭，略過
			continue
		}

		cur := close[e].(float64)

		min := cur
		isNewLow := 1
		for k := e; k >= s; k-- {
			if close[k] == nil {
				continue
			}
			if close[k].(float64) < min {
				isNewLow = 0
				break
			}
		}
		isNewLows[currentDate] = isNewLow

		// 單日跌幅 >= 9%(接近跌停)：跟前一個有效收盤比較
		isSharpDrop := 0
		for k := e - 1; k >= 0; k-- {
			if close[k] == nil {
				continue
			}
			prev := close[k].(float64)
			if prev > 0 && (cur-prev)/prev <= -0.09 {
				isSharpDrop = 1
			}
			break
		}
		isSharpDrops[currentDate] = isSharpDrop
	}

	shift = 0
	isNewHighs := map[string]int{}
	// 開始計算股價高於X日的結果
	for {
		e := closeLen - shift
		s := e - window
		shift++

		if s <= 0 {
			break
		}

		// close[e] 為 nil 代表該股當天沒交易，跳過不計(Excel 留空)，不填 0
		if close[e] == nil {
			continue
		}

		currentDate := time.Unix(int64(timestamp[e]), 0).Format("2006/01/02")
		if contains(nilCloseDates, currentDate) { //大盤沒開盤的日子不在表頭，略過
			continue
		}

		max := close[e].(float64)
		isNewHigh := 1
		for k := e; k >= s; k-- {
			if close[k] == nil {
				continue
			}
			if close[k].(float64) > max {
				isNewHigh = 0
				break
			}
		}
		isNewHighs[currentDate] = isNewHigh
	}

	var d Data
	d.OptionNum = stockInfo.Code
	d.IsNewLows = isNewLows
	d.IsNewHighs = isNewHighs
	d.IsSharpDrops = isSharpDrops
	*allRes = append(*allRes, d)
}

func contains(slice []string, value string) bool {
	for _, v := range slice {
		if v == value {
			return true
		}
	}
	return false
}

// buildParticipation 統計每個日期有多少檔個股有非 null 收盤（真正有交易的家數）
func buildParticipation(stockInfos []StockInfo) map[string]int {
	participation := map[string]int{}
	for _, si := range stockInfos {
		body, err := ioutil.ReadFile("data/" + si.Code)
		if err != nil {
			continue
		}
		var stock Stock
		if json.Unmarshal(body, &stock) != nil {
			continue
		}
		if len(stock.Chart.Result) == 0 {
			continue
		}
		ts := stock.Chart.Result[0].Timestamp
		closes := stock.Chart.Result[0].Indicators.Quote[0].Close
		for i, t := range ts {
			if i < len(closes) && closes[i] != nil {
				date := time.Unix(int64(t), 0).Format("2006/01/02")
				participation[date]++
			}
		}
	}
	return participation
}

// findLowParticipationDates 找出參與度遠低於鄰近交易日的異常日期（Yahoo 資料缺漏）。
// 跟前後鄰近交易日的中位數比較，避免誤傷「早期上市家數本來就少」的正常歷史。
func findLowParticipationDates(orderedDates []string, participation map[string]int) []string {
	const window = 10          // 前後各約 10 個交易日
	const ratioThreshold = 0.2 // 低於鄰近中位數的 20% 視為異常
	var anomalies []string
	n := len(orderedDates)
	for i, date := range orderedDates {
		var neighbors []int
		for j := i - window; j <= i+window; j++ {
			if j < 0 || j >= n || j == i {
				continue
			}
			neighbors = append(neighbors, participation[orderedDates[j]])
		}
		if len(neighbors) == 0 {
			continue
		}
		sort.Ints(neighbors)
		med := neighbors[len(neighbors)/2]
		if med > 0 && float64(participation[date]) < float64(med)*ratioThreshold {
			anomalies = append(anomalies, date)
		}
	}
	return anomalies
}

func dfs(node *html.Node, stockInfos *[]StockInfo, strMode string) {
	if node.Type == html.ElementNode && node.Data == "tr" {
		var code string
		var cfiCode string

		tdCount := 0
		for c := node.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "td" {
				tdCount++
				if tdCount == 3 {
					code = c.FirstChild.Data
				} else if tdCount == 9 {
					cfiCode = c.FirstChild.Data
				}
			}
		}

		if cfiCode == "ESVUFR" {
			code = code[:4]
			stockInfo := StockInfo{Code: code, Type: strMode}
			*stockInfos = append(*stockInfos, stockInfo)
		}
	}

	for c := node.FirstChild; c != nil; c = c.NextSibling {
		dfs(c, stockInfos, strMode)
	}
}

func downloadStockCodes(url string) ([]StockInfo, error) {

	response, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	doc, err := html.Parse(response.Body)
	if err != nil {
		fmt.Println("Error parsing HTML:", err)
		return nil, err
	}

	var stockInfos []StockInfo
	strMode := string(url[len(url)-1])
	dfs(doc, &stockInfos, strMode)

	return stockInfos, nil
}

func DownloadStockList() ([]StockInfo, error) {
	fmt.Println("下載上市櫃公司清單中...")

	stockInfos := []StockInfo{}

	urls := []string{
		"https://isin.twse.com.tw/isin/class_main.jsp?Page=1&chklike=Y&market=1&issuetype=1", // 上市
		"https://isin.twse.com.tw/isin/class_main.jsp?Page=1&chklike=Y&market=2&issuetype=4", // 上櫃
	}

	for _, url := range urls {
		infos, err := downloadStockCodes(url)
		if err != nil {
			return nil, err
		}
		stockInfos = append(stockInfos, infos...)
	}

	return stockInfos, nil
}
