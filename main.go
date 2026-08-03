package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/xuri/excelize/v2"
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
	OptionNum  string
	IsNewLows  map[string]int // 日期 -> 是否創新低(1/0)，該股當天沒交易則不存在此 key
	IsNewHighs map[string]int // 日期 -> 是否創新高(1/0)，該股當天沒交易則不存在此 key
	Url        string
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
}

// WebOutput 是 web/data.json 的完整結構
type WebOutput struct {
	DayParameter int         `json:"dayParameter"`
	Generated    string      `json:"generated"`
	Days         []MarketDay `json:"days"`
}

var dataRange string = "10y" // 1y 5y 10y 20y

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

	fmt.Printf("\n\n初始化Excel中...\n")
	xlsxName := fmt.Sprintf("股價創%d日新高新低比例.xlsx", dayParameter)
	var f *excelize.File
	if _, statErr := os.Stat(xlsxName); statErr == nil {
		// 已有既存檔案就開啟沿用
		f, err = excelize.OpenFile(xlsxName)
		if err != nil {
			panic(err)
		}
	} else {
		// 檔案不存在（例如 CI 環境）就新建，後續會自行建立所需工作表
		f = excelize.NewFile()
	}
	defer func() {
		f.SaveAs(xlsxName)
		f.Close()
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
	sheetMarket := "大盤"
	sheetStockInXDayLOW := fmt.Sprintf("股價創%d日新低個股", dayParameter)
	sheetStockInXDayHIGH := fmt.Sprintf("股價創%d日新高個股", dayParameter)
	f.DeleteSheet(sheetMarket)
	f.DeleteSheet(sheetStockInXDayLOW)
	f.DeleteSheet(sheetStockInXDayHIGH)

	idxMarket := f.NewSheet(sheetMarket)
	f.NewSheet(sheetStockInXDayLOW)
	f.NewSheet(sheetStockInXDayHIGH)
	f.SetActiveSheet(idxMarket)
	f.DeleteSheet("Sheet1") // 移除 NewFile() 產生的預設空白表；OpenFile 情況下不存在，無副作用

	Header(f, sheetMarket, sheetStockInXDayLOW, sheetStockInXDayHIGH)

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

	// 大盤Sheet
	//寫入大盤資料
	k := 0
	for i, t := range tseTimestamp {

		date := time.Unix(int64(t), 0).Format("2006/01/02")
		volumneValue := tse.Chart.Result[0].Indicators.Quote[0].Volume[i]
		openValue := tse.Chart.Result[0].Indicators.Quote[0].Open[i]
		highValue := tse.Chart.Result[0].Indicators.Quote[0].High[i]
		lowValue := tse.Chart.Result[0].Indicators.Quote[0].Low[i]
		closeValue := tse.Chart.Result[0].Indicators.Quote[0].Close[i]

		if contains(nilCloseDates, date) { //沒開盤就不寫入Sheet
			continue
		}

		// 大盤Sheet
		// 從第二列開始
		f.SetCellValue(sheetMarket, fmt.Sprintf("A%d", k+2), date)
		f.SetCellValue(sheetMarket, fmt.Sprintf("B%d", k+2), volumneValue)
		f.SetCellValue(sheetMarket, fmt.Sprintf("C%d", k+2), openValue)
		f.SetCellValue(sheetMarket, fmt.Sprintf("D%d", k+2), highValue)
		f.SetCellValue(sheetMarket, fmt.Sprintf("E%d", k+2), lowValue)
		f.SetCellValue(sheetMarket, fmt.Sprintf("F%d", k+2), closeValue)
		k++
	}

	// 個股Sheet
	// 填日期從B1往右填，並記錄欄位順序供後續對齊
	k = 2
	var orderedDates []string
	for i := len(tseTimestamp) - 1; i >= 0; i-- {
		date := time.Unix(int64(tseTimestamp[i]), 0).Format("2006/01/02")
		if contains(nilCloseDates, date) { //沒開盤就不寫入Sheet
			continue
		}
		c, _ := excelize.CoordinatesToCellName(k, 1)
		f.SetCellValue(sheetStockInXDayLOW, c, date)
		f.SetCellValue(sheetStockInXDayHIGH, c, date)
		orderedDates = append(orderedDates, date)
		k++
	}

	fmt.Printf("最高最低計算中...\n")
	allRes := []Data{}
	for i, stockInfo := range filterStockInfos {
		DoCalc(stockInfo, &allRes, nilCloseDates)
		fmt.Printf("\r(%d/%d)", i+1, size)
	}

	// 按照股號先排序
	sort.Slice(allRes, func(i, j int) bool {
		return allRes[i].OptionNum < allRes[j].OptionNum
	})

	// 個股Sheet
	fmt.Println("將全市場每日低於股價創60日新低的結果填入...")
	for i, d := range allRes {

		f.SetCellValue(sheetStockInXDayLOW, fmt.Sprintf("A%d", i+2), d.OptionNum)

		// 照大盤表頭的日期逐欄對齊；該股當天沒交易(map 沒有此日期)就留空，不填 0
		for col, date := range orderedDates {
			n, ok := d.IsNewLows[date]
			if !ok {
				continue
			}
			c, _ := excelize.CoordinatesToCellName(col+2, i+2)
			f.SetCellValue(sheetStockInXDayLOW, c, n)
		}

		fmt.Printf("\r(%d/%d)", i+1, len(allRes))
	}
	fmt.Println()

	fmt.Println("依照日期統計股價創60日新低的家數...")
	sumInXDayLow := map[string]int{}
	rowsStock, _ := f.GetRows(sheetStockInXDayLOW)
	for j := 2; j <= len(rowsStock[0]); j++ {
		c, _ := excelize.CoordinatesToCellName(j, 1)
		d, _ := f.GetCellValue(sheetStockInXDayLOW, c)

		for i := 2; i <= len(rowsStock); i++ {
			c2, _ := excelize.CoordinatesToCellName(j, i)
			d2, _ := f.GetCellValue(sheetStockInXDayLOW, c2)
			v, _ := strconv.Atoi(d2)
			sumInXDayLow[d] += v
		}

		fmt.Printf("\r(%d/%d)", j, len(rowsStock[0]))
	}
	fmt.Println()

	fmt.Println("將全市場每日高於股價創60日新高的結果填入...")
	for i, d := range allRes {

		f.SetCellValue(sheetStockInXDayHIGH, fmt.Sprintf("A%d", i+2), d.OptionNum)

		// 照大盤表頭的日期逐欄對齊；該股當天沒交易(map 沒有此日期)就留空，不填 0
		for col, date := range orderedDates {
			n, ok := d.IsNewHighs[date]
			if !ok {
				continue
			}
			c, _ := excelize.CoordinatesToCellName(col+2, i+2)
			f.SetCellValue(sheetStockInXDayHIGH, c, n)
		}

		fmt.Printf("\r(%d/%d)", i+1, len(allRes))
	}
	fmt.Println()

	fmt.Println("依照日期統計股價創60日新高的家數...")
	sumInXDayHighCount := map[string]int{}
	sumInXDayHigh := map[string]int{}
	rowsStock, _ = f.GetRows(sheetStockInXDayHIGH)
	for j := 2; j <= len(rowsStock[0]); j++ {
		c, _ := excelize.CoordinatesToCellName(j, 1)
		d, _ := f.GetCellValue(sheetStockInXDayHIGH, c)

		for i := 2; i <= len(rowsStock); i++ {
			c2, _ := excelize.CoordinatesToCellName(j, i)
			d2, _ := f.GetCellValue(sheetStockInXDayHIGH, c2)
			v, err := strconv.Atoi(d2)
			sumInXDayHigh[d] += v
			if err == nil {
				sumInXDayHighCount[d]++
			}
		}

		fmt.Printf("\r(%d/%d)", j, len(rowsStock[0]))
	}
	fmt.Println()

	// 大盤Sheet
	fmt.Println("將統計結果填入...")
	rowsTse, _ := f.GetRows(sheetMarket)
	var webDays []MarketDay
	for i := 2; i <= len(rowsTse); i++ {
		d, _ := f.GetCellValue(sheetMarket, fmt.Sprintf("A%d", i))
		f.SetCellValue(sheetMarket, fmt.Sprintf("G%d", i), sumInXDayLow[d])
		f.SetCellValue(sheetMarket, fmt.Sprintf("H%d", i), sumInXDayHigh[d])
		f.SetCellValue(sheetMarket, fmt.Sprintf("I%d", i), sumInXDayHighCount[d])

		var lowP, highP float64
		if sumInXDayHighCount[d] == 0 {
			f.SetCellValue(sheetMarket, fmt.Sprintf("J%d", i), 0)
			f.SetCellValue(sheetMarket, fmt.Sprintf("K%d", i), 0)
		} else {
			lowP = float64(sumInXDayLow[d]) / float64(sumInXDayHighCount[d]) * 100
			highP = float64(sumInXDayHigh[d]) / float64(sumInXDayHighCount[d]) * 100
			f.SetCellValue(sheetMarket, fmt.Sprintf("J%d", i), lowP)
			f.SetCellValue(sheetMarket, fmt.Sprintf("K%d", i), highP)
		}

		// 同步收集給前端網頁用的資料
		closeStr, _ := f.GetCellValue(sheetMarket, fmt.Sprintf("F%d", i))
		closeVal, _ := strconv.ParseFloat(closeStr, 64)
		webDays = append(webDays, MarketDay{
			Date:      d,
			Close:     closeVal,
			LowCount:  sumInXDayLow[d],
			HighCount: sumInXDayHigh[d],
			Total:     sumInXDayHighCount[d],
			LowPct:    lowP,
			HighPct:   highP,
		})

		fmt.Printf("\r(%d/%d)", i, len(rowsTse))

	}
	fmt.Println()

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

		min := close[e].(float64)
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

func ClearCells(f *excelize.File, sheet string) {
	rows, _ := f.GetRows(sheet)
	for i, row := range rows {
		for j := range row {
			c, _ := excelize.CoordinatesToCellName(j+1, i+1)
			f.SetCellValue(sheet, c, "")
		}
	}
}

func Header(f *excelize.File, sheet1 string, sheet2 string, sheet3 string) {
	f.SetCellValue(sheet1, "A1", "日期")
	f.SetCellValue(sheet1, "B1", "成交量")
	f.SetCellValue(sheet1, "C1", "開盤價")
	f.SetCellValue(sheet1, "D1", "最高價")
	f.SetCellValue(sheet1, "E1", "最低價")
	f.SetCellValue(sheet1, "F1", "收盤價")
	f.SetCellValue(sheet1, "G1", fmt.Sprintf("股價低於%d日家數", dayParameter))
	f.SetCellValue(sheet1, "H1", fmt.Sprintf("股價高於%d日家數", dayParameter))
	f.SetCellValue(sheet1, "I1", "家數")
	f.SetCellValue(sheet1, "I1", "家數")
	f.SetCellValue(sheet1, "J1", fmt.Sprintf("股價低於%d日家數比例", dayParameter))
	f.SetCellValue(sheet1, "K1", fmt.Sprintf("股價高於%d日家數比例", dayParameter))

	f.SetCellValue(sheet2, "A1", "股號")

	f.SetCellValue(sheet3, "A1", "股號")
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
