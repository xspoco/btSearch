package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"runtime"
	"strconv"
	"sync"

	mgo "gopkg.in/mgo.v2"
	"gopkg.in/mgo.v2/bson"
)

var perDataSize = 50
var maxThreadNum = 100

const (
	esURL = "http://127.0.0.1:9200/bavbt/torrent/"

	mongoAddr  = "127.0.0.1:27017"
	dataBase   = "bavbt"
	collection = "torrent"
)

type monServer struct {
	printChan chan string
	Client    http.Client
	Session   *mgo.Session
	Data      chan []map[string]interface{}
	wg        *sync.WaitGroup
	queue     chan int
}

type esData struct {
	Title      string
	ObjectID   string
	Length     int64
	CreateTime int64
	FileType   string
	Hot        int
}

func newMon() *monServer {
	client := http.Client{
		Transport: &http.Transport{
			MaxIdleConns:        20,
			MaxIdleConnsPerHost: 20,
			// Dial: func(netw, addr string) (net.Conn, error) {
			// 	deadline := time.Now().Add(1 * time.Second)
			// 	c, err := net.DialTimeout(netw, addr, time.Second*1)
			// 	if err != nil {
			// 		return nil, err
			// 	}
			// 	c.SetDeadline(deadline)
			// 	return c, nil
			// },
			//DisableKeepAlives: false,
		},
	}
	dialInfo := &mgo.DialInfo{
		Addrs:  []string{mongoAddr},
		Direct: false,
		//Timeout: time.Second * 1,
		Database: dataBase,
		Source:   collection,
		// Username:  "root",
		// Password:  "root",
		//PoolLimit: 4096, // Session.SetPoolLimit
	}

	session, err := mgo.DialWithInfo(dialInfo)
	if err != nil {
		panic(err.Error())
	}
	session.SetPoolLimit(40)
	session.SetMode(mgo.Monotonic, true)
	return &monServer{
		printChan: make(chan string, 5),
		Client:    client,
		Session:   session,
		Data:      make(chan []map[string]interface{}, 1000),
		wg:        &sync.WaitGroup{},
		queue:     make(chan int, 20),
	}
}

func (m *monServer) getdata(objectid bson.ObjectId) {

	//data := bson.M{"hot": 100}
	c := m.Session.DB("bavbt").C("torrent")
	for {

		data := make([]map[string]interface{}, perDataSize)
		selector := bson.M{"_id": map[string]bson.ObjectId{"$gt": objectid}}
		c.Find(selector).Limit(perDataSize).All(&data)
		// for _, i := range data {
		// 	m.printChan <- (i["_id"])

		// }
		m.Data <- data
		//m.printChan <- (len(data))
		if size := len(data); size == perDataSize {
			objectid = data[size-1]["_id"].(bson.ObjectId)
		} else {
			m.printChan <- ("Done!!!")
			break
		}

	}

}
func (m *monServer) Add(delta int) {
	for i := 0; i < delta; i++ {
		m.queue <- 1
	}
	for i := 0; i > delta; i-- {
		<-m.queue
	}
	m.wg.Add(delta)
}

func (m *monServer) Done() {
	<-m.queue
	m.wg.Done()
}

func (m *monServer) Wait() {
	m.wg.Wait()
}
func (m *monServer) Put(url string, data []byte, pid int, maxThread chan int) (err error) {
	m.Add(1)
	req, err := http.NewRequest("PUT", url, bytes.NewReader(data))
	req.Header.Set("Content-Type", "application/json;charset=UTF-8")
	resp, err := m.Client.Do(req)
	if err != nil {
		m.Done()
		m.printChan <- ("Try Again ERROR2222c:" + err.Error())
		return m.Put(url, data, pid, maxThread)
	}
	io.Copy(ioutil.Discard, resp.Body)
	//m.printChan <- (string(body))
	if resp.StatusCode != 201 && resp.StatusCode != 200 {
		fmt.Println(resp.StatusCode)
		resp.Body.Close()
		m.Done()
		// handle error
		m.printChan <- ("Try Again ERROR11112121")

		return m.Put(url, data, pid, maxThread)
	}
	resp.Body.Close()
	//m.printChan <- (string(body))
	maxThread <- pid
	m.Done()
	return err

}

func (m *monServer) sync() {

	//var num int64 = 0

	maxThread := make(chan int, maxThreadNum)

	for i := 1; i <= maxThreadNum; i++ {
		maxThread <- i

	}

	for i := range m.Data {
		go m.parserData(i, maxThread)
		m.printChan <- ("-----------------------------------------------")
	}
}

func (m *monServer) parserData(data []map[string]interface{}, maxThread chan int) {
	for _, one := range data {

		if _, ok := one["name"]; !ok {
			m.printChan <- ("Name Err")
			return
		}
		if _, ok := one["_id"]; !ok {
			m.printChan <- ("_id Err")
			return
		}

		if _, ok := one["length"]; !ok {
			m.printChan <- ("length Err")
			return
		}
		if _, ok := one["create_time"]; !ok {
			m.printChan <- ("create_time Err")
			return
		}
		if _, ok := one["category"]; !ok {
			m.printChan <- ("Category Err")
			return
		}
		if _, ok := one["hot"]; !ok {
			m.printChan <- ("Hot Err")
			return
		}

		syncdata, err := json.Marshal(esData{
			Title:      one["name"].(string),
			ObjectID:   one["_id"].(bson.ObjectId).Hex(),
			Length:     one["length"].(int64),
			CreateTime: one["create_time"].(int64),
			FileType:   firstUpper(one["category"].(string)),
			Hot:        one["hot"].(int),
		})
		if err != nil {
			return
		}
		pid := <-maxThread
		m.printChan <- ("PID:" + strconv.Itoa(pid) + "----" + "---" + one["name"].(string) + "------" + one["_id"].(bson.ObjectId).Hex())

		go m.Put(esURL+one["infohash"].(string), syncdata, pid, maxThread)

	}
}
func firstUpper(str string) string {
	if str == "" {
		return str

	}
	first := int(str[0])
	if 96 < first && first < 123 {
		return string(first-32) + str[1:]

	}
	return str
}
func (m *monServer) PrintLog() {

	for {
		fmt.Println(<-m.printChan)
	}

}

func (m *monServer) run() (data map[string]interface{}) {

	m.printChan <- ("Runing...")
	c := m.Session.DB("bavbt").C("torrent")
	//selector := bson.M{} //从0开始
	selector := bson.M{"_id": bson.ObjectIdHex("5abe367b5ddbf96ea5a39c4f")}
	//中断开始
	c.Find(selector).Sort("_id").Limit(1).One(&data)
	maxThread := make(chan int, 1)
	maxThread <- 1
	m.parserData([]map[string]interface{}{data}, maxThread)
	return data
}

func main() {
	runtime.GOMAXPROCS(runtime.NumCPU())
	m := newMon()
	defer m.Session.Close()
	go m.PrintLog()
	entry := m.run()
	go m.getdata(entry["_id"].(bson.ObjectId))
	m.sync()

}
