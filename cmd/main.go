package main

import (
	"container/heap"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/hoenigmann/logmon/parse"
	"github.com/hpcloud/tail"
	sw "github.com/prep/average"
)

var flagLogFile string
var flagHelp bool
var flagRatePerSec int

func init() {
	flag.StringVar(&flagLogFile, "f", "/var/log/access.log", "log file")
	flag.BoolVar(&flagHelp, "h", false, "print version")
	flag.IntVar(&flagRatePerSec, "r", 10, "rate per sec over two minutes to trigger alert")
}

// Used:
// ~/proj/go/bin/ldetool generate $GOPATH/src/github.com/ahoenigmann/parse/logparse.lde --go-string --package parse//~/proj/go/bin/ldetool generate $GOPATH/src/github.com/ahoenigmann/parse/logparse.lde --go-string --package parse
// to generate parser
func main() {
	flag.Parse()
	if flagHelp {
		flag.PrintDefaults()
		os.Exit(0)
	}
	t, err := tail.TailFile(flagLogFile, tail.Config{
		Follow:    true,
		ReOpen:    true,
		MustExist: true},
	)

	if err != nil {
		fmt.Println(err)
		panic("Exiting File error")
	}

	swTotal2Minute := sw.MustNew(2*time.Minute, 1*time.Second)
	defer swTotal2Minute.Stop()

	site := NewSite("DataDog")

	go site.every10Seconds()
	go site.AlertHighTraffic()

	var line *tail.Line
	for line = range t.Lines {
		fmt.Println(line.Text)
		parse := new(parse.Line)
		parse.Extract(line.Text)
		site.MonitorLine(parse)
	}
}

type Site struct {
	Name                  string
	window2Min            *sw.SlidingWindow
	window2xxResponseCode *sw.SlidingWindow
	window3xxResponseCode *sw.SlidingWindow
	window4xxResponseCode *sw.SlidingWindow
	window5xxResponseCode *sw.SlidingWindow
	sections              map[string]*Section
}

func NewSite(name string) *Site {
	site := new(Site)
	site.Name = name
	site.window2Min = sw.MustNew(2*time.Minute, 1*time.Second)
	site.window2xxResponseCode = sw.MustNew(10*time.Second, 1*time.Second)
	site.window3xxResponseCode = sw.MustNew(10*time.Second, 1*time.Second)
	site.window4xxResponseCode = sw.MustNew(10*time.Second, 1*time.Second)
	site.window5xxResponseCode = sw.MustNew(10*time.Second, 1*time.Second)
	site.sections = make(map[string]*Section)
	return site
}

func (s *Site) MonitorLine(line *parse.Line) {
	s.window2Min.Add(1)

	sectionName := getSection(line.Path)

	ALERT_MAX := 10

	if _, ok := s.sections[sectionName]; !ok {
		section := NewSection(sectionName, ALERT_MAX)
		s.sections[sectionName] = section
	}

	s.Monitor(line.ResponseCode)
	s.sections[sectionName].Monitor(line.ResponseCode)

}

func (s *Site) Monitor(responseCode int) {
	switch {
	case responseCode > 199 && responseCode < 300:
		s.window2xxResponseCode.Add(1)
	case responseCode > 499 && responseCode < 600:
		s.window5xxResponseCode.Add(1)
	case responseCode > 399 && responseCode < 500:
		s.window4xxResponseCode.Add(1)
	case responseCode > 299 && responseCode < 400:
		s.window3xxResponseCode.Add(1)
	default:
		fmt.Println("found a weird response code")
	}
}

func (s *Site) AlertHighTraffic() {
	time.Sleep(2 * time.Second)
	triggered := false
	for {
		time.Sleep(1 * time.Second)
		avgOverTwoMin := s.window2Min.Average(2 * time.Minute)
		//fmt.Println("avg over two min: ", avgOverTwoMin)
		if avgOverTwoMin > float64(flagRatePerSec) && !triggered {
			count, _ := s.window2Min.Total(2 * time.Minute)
			t := time.Now()
			color.Set(color.FgRed)
			fmt.Printf("\nHigh traffic generated an alert - hits = %v, triggered at %v\n", count, t.Format("2006-01-02 15:04:05"))
			fmt.Printf("(Above %v request per second over the past two minute interval)\n\n", flagRatePerSec)
			color.Unset()
			triggered = true
		}

		if avgOverTwoMin < float64(flagRatePerSec) && triggered {
			count, _ := s.window2Min.Total(2 * time.Minute)
			t := time.Now()
			color.Set(color.FgBlue)
			fmt.Printf("\nRECOVERY: from High traffic alert - hits = %v, recovered at %v\n\n", count, t.Format("2006-01-02 15:04:05"))
			color.Unset()
			triggered = false
		}
	}
}

func getSection(path string) string {
	if len(path) > 1 {
		index := strings.Index(path, "/")
		if index < len(path) && index > -1 {
			index = strings.Index(path[1:], "/")
			if index > -1 {
				return path[:index+1]
			} else {
				return path
			}
		}
	}
	return ""

}

type Section struct {
	Name                  string
	metric                chan parse.Line
	window10Sec           *sw.SlidingWindow
	window2xxResponseCode *sw.SlidingWindow
	window3xxResponseCode *sw.SlidingWindow
	window4xxResponseCode *sw.SlidingWindow
	window5xxResponseCode *sw.SlidingWindow
	AlertMax              int
	priority              int
	index                 int
}

func NewSection(name string, alertMax int) *Section {
	s := new(Section)
	s.metric = make(chan parse.Line)
	s.window10Sec = sw.MustNew(10*time.Second, 1*time.Second)
	s.Name = name
	s.AlertMax = alertMax
	return s
}

func (s *Section) Monitor(responseCode int) {
	s.window10Sec.Add(1)
}

func (s *Section) PriorityCount() int64 {
	a, _ := s.window10Sec.Total(10 * time.Second)
	s.priority = int(a)
	return a
}

func (s *Site) every10Seconds() {
	for {
		color.Set(color.FgGreen)
		fmt.Println("===Summary Statistics for Past 10 seconds of activity===")
		s.PrintSummaryStats()
		color.Unset()
		time.Sleep(10 * time.Second)
	}
}

func (s *Site) PrintSummaryStats() {
	s.printTopSections()
	s.printResponseCodes()
}

func (s *Site) printResponseCodes() {
	t2xx, _ := s.window2xxResponseCode.Total(10 * time.Second)
	t3xx, _ := s.window3xxResponseCode.Total(10 * time.Second)
	t4xx, _ := s.window4xxResponseCode.Total(10 * time.Second)
	t5xx, _ := s.window5xxResponseCode.Total(10 * time.Second)
	fmt.Printf("\tResponse Codes:\n2xx: %v, 3xx: %v, 4xx: %v, 5xx: %v\n", t2xx, t3xx, t4xx, t5xx)
}

func (s *Site) printTopSections() {
	fmt.Printf("Top 5 sections of the site past 10 seconds: \n")
	pq := make(PriorityQueue, 0)
	heap.Init(&pq)

	for _, v := range s.sections {
		work := v
		work.PriorityCount() // Sets count to priority in Section
		heap.Push(&pq, work)
	}

	if len(s.sections) <= 0 {
		fmt.Println("-No traffic yet-")
	}

	for i := 0; i < len(s.sections); i++ {
		top := heap.Pop(&pq).(*Section)
		fmt.Println(top.Name, ": ", top.priority)
		if i > 4 {
			break
		}
	}
	fmt.Println("-------------------------------")
}

//====================================Priority Queue Implementation===================/
type PriorityQueue []*Section

func (pq PriorityQueue) Len() int {
	return len(pq)
}

func (pq PriorityQueue) Less(i, j int) bool {
	// We want Pop to give us the highest, not lowest, priority so use greater than here.
	return pq[i].priority > pq[j].priority
}

func (pq PriorityQueue) Swap(i, j int) {
	pq[i], pq[j] = pq[j], pq[i]
	pq[i].index = i
	pq[j].index = j
}

func (pq *PriorityQueue) Push(x interface{}) {
	n := len(*pq)
	item := x.(*Section)
	item.index = n
	*pq = append(*pq, item)
}

func (pq *PriorityQueue) Pop() interface{} {
	old := *pq
	n := len(old)
	item := old[n-1]
	item.index = -1 // for safety
	*pq = old[0 : n-1]
	return item
}

// update modifies the priority and value of an Item in the queue.
func (pq *PriorityQueue) update(item *Section, value string, priority int) {
	//item.value = value
	item.priority = int(item.PriorityCount())
	heap.Fix(pq, item.index)
}
