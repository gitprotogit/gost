package main

import (
	"os"
	"net"
	"time"
	"sort"
	"bytes"
	"unsafe"
	"net/url"
	"strings"
	"strconv"
	"crypto/md5"
	"crypto/tls"
	"encoding/hex"
	
	"github.com/gitprotogit/gost"
	"github.com/json-iterator/go"
	"github.com/parnurzeal/gorequest"
	"github.com/eclipse/paho.mqtt.golang"
	
	"fmt"
)

var MqttClient mqtt.Client

func main() {
	
	var interfaceName = os.Getenv("SPROXY_INTERFACE")
	if interfaceName == "" {
		interfaceName = "br-lan"
	}
	interfaceName = "en0"
	var host = os.Getenv("SPROXY_HOST")
	if host == "" {
		host = "http://127.0.0.1:9220"
	}
	var proxyAddr = os.Getenv("SPROXY_ADDR")
	if proxyAddr == "" {
		proxyAddr = ":18121"
	}
	var appId = os.Getenv("SPROXY_APPID")
	if appId == "" {
		appId = "101235"
	}
	var secret = os.Getenv("SPROXY_SECRET")
	if secret == "" {
		secret = "UIqN4W6uA1EF4AuPrfPb24RlwPED8x9dkHaxearJgQkPc2Sr39I37cE7ZEhYoWg4"
	}
	
	var interfaceMac = ""
	//开始 获取DNS
	netInterfaces, err := net.Interfaces()
	if err != nil {
		fmt.Printf("fail to get net interfaces: %s", err.Error())
		return
	}
	for _, netInterface := range netInterfaces {
		if netInterface.Name != interfaceName {
			continue
		}
		macAddr := netInterface.HardwareAddr.String()
		if len(macAddr) == 0 {
			continue
		}
		interfaceMac = strings.ToUpper(strings.Replace(macAddr, ":", "", -1))
		break
	}
	if interfaceMac == "" {
		fmt.Printf("fail to get net interfaces by %s", interfaceName)
		return
	}
	//结束 获取DNS
	
	//开始 配置参数加密
	var urlValues = url.Values{}
	urlValues.Add("app_id", appId)
	urlValues.Add("st", strconv.FormatInt(time.Now().Unix(), 10))
	urlValues.Add("device_id", interfaceMac)
	
	keys := make([]string, len(urlValues))
	i := 0
	for k := range urlValues {
		keys[i] = k
		i++
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	for _, k := range keys {
		if k != "sign" && len(urlValues[k]) > 0 && urlValues[k][0] != "" {
			buf.WriteString(k)
			buf.WriteString("=")
			buf.WriteString(urlValues[k][0])
			buf.WriteString("&")
		}
	}
	buf.WriteString("secret=")
	buf.WriteString(secret)
	urlValues.Add("sign", strings.ToUpper(mD5EncodeToString(buf.String())))
	//结束 配置参数加密
	
	var config Config
	//开始 读取配置信息
	request := gorequest.New()
	resp, _, errs := request.Get(host + "?" + urlValues.Encode()).Timeout(30 * time.Second).End()
	if errs != nil {
		fmt.Printf("fail to get conf err %v", errs)
		//return
	}
	if resp != nil {
		var encoder = jsoniter.ConfigCompatibleWithStandardLibrary.NewDecoder(resp.Body)
		err = encoder.Decode(&config)
		if err != nil {
			fmt.Printf("fail to get conf Decode err %s", err.Error())
			//return
		}
	}
	//结束 截取配置信息
	
	//开始 连接mqtt
	if config.Info.Host != "" {
		opts := mqtt.NewClientOptions()
		opts.AddBroker(config.Info.Host)
		opts.SetAutoReconnect(true)
		opts.SetKeepAlive(5 * time.Second)
		opts.SetOnConnectHandler(onConnectHandler)
		opts.SetConnectionLostHandler(connectionLostHandler)
		opts.SetClientID("SProxy" + interfaceMac)
		opts.SetCleanSession(false)
		
		MqttClient = mqtt.NewClient(opts)
	
	ConnRetry:
		if token := MqttClient.Connect(); token.Wait() && token.Error() != nil {
			fmt.Printf("fail to mqtt Connect err %s", token.Error().Error())
			goto ConnRetry
		}
	
	SubRetry:
		if token := MqttClient.Subscribe(config.Info.Sub, byte(0), subMessageHandler); token.Wait() && token.Error() != nil {
			fmt.Printf("fail to mqtt Subscribe err %s", token.Error().Error())
			goto SubRetry
		}
	}
	//结束 连接mqtt
	
	//开始 连接代理
	cert, err := gost.GenCertificate()
	if err != nil {
		fmt.Printf("fail to GenCertificate err %s", err.Error())
		return
	}
	gost.DefaultTLSConfig = &tls.Config{
		Certificates: []tls.Certificate{cert},
	}
	
	var route Route
	if err := route.serve(proxyAddr); err != nil {
		fmt.Printf("fail to serve err %s", err.Error())
		os.Exit(1)
	}
	
	select {}
	//结束 连接代理
}

func (r *Route) serve(listenAddr string) error {
	var ln gost.Listener
	var err error
	ln, err = gost.TCPListener(listenAddr)
	if err != nil {
		fmt.Printf("fail to TCPListener err %s", err.Error())
		return err
	}
	var handler = gost.TcpRedirectSPHandler()
	srv := &gost.Server{Listener: ln}
	go srv.Serve(handler)
	return nil
}

type Route struct {
	ChainNodes, ServeNodes stringList
	Retries                int
	Debug                  bool
}

type stringList []string

func (l *stringList) String() string {
	return fmt.Sprintf("%s", *l)
}
func (l *stringList) Set(value string) error {
	*l = append(*l, value)
	return nil
}

type Config struct {
	ErrCode    int        `json:"err_code"`
	ErrMsg     string     `json:"err_msg"`
	ResultCode string     `json:"result_code"`
	ReturnCode string     `json:"return_code"`
	ReturnMsg  string     `json:"return_msg"`
	Info       ConfigInfo `json:"info"`
}

type ConfigInfo struct {
	Host string `json:"host"`
	Pub  string `json:"pub"`
	Sub  string `json:"sub"`
}

func onConnectHandler(client mqtt.Client) {
}

func connectionLostHandler(client mqtt.Client, err error) {
}

func subMessageHandler(client mqtt.Client, msg mqtt.Message) {
	msgStr := string(msg.Payload())
	//过滤无意义信息
	if !strings.HasPrefix(msgStr, "{") {
		return
	}
	topic := msg.Topic()
	var mqttMsgBase MqttMsgBase
	var encoder = jsoniter.ConfigCompatibleWithStandardLibrary.NewDecoder(strings.NewReader(string(msg.Payload())))
	err := encoder.Decode(&mqttMsgBase)
	if err != nil {
		fmt.Printf("subMessageHandler topic:%s, msg:%s, Unmarshal Base, err:%s", topic, msgStr, err.Error())
		return
	}
	go parseProcess(mqttMsgBase.Action, mqttMsgBase.Strict, topic, msgStr, &mqttMsgBase)
}

func parseProcess(mqttAction string, strict bool, topic string, msgStr string, mqttMsgBase *MqttMsgBase) {
	
	var parseMap = map[string]func(topic string, msg string, mqttMsgBase *MqttMsgBase) error{
	
	}
	//切分action
	fun, ok := parseMap[mqttAction]
	if !ok {
		fmt.Printf("parseProcess parseMap topic:%s, msg:%s, No Action Msg", topic, msgStr)
		return
	}
	//如果需要回复
	if strict {
		if beforeFun, ok := parseMap[mqttAction+"_rr"]; ok {
			beforeFun(topic, msgStr, mqttMsgBase)
		}
		//send processing
	}
	err := fun(topic, msgStr, mqttMsgBase)
	if err != nil {
		fmt.Printf("parseProcess fun topic:%s, msg:%s, Unmarshal Base, err:%s", topic, msgStr, err.Error())
		return
	}
	if strict {
		//send complete
		if err == nil {
			if afterFun, ok := parseMap[mqttAction+"_rs"]; ok {
				afterFun(topic, msgStr, mqttMsgBase)
			}
		}
		return
	}
}

type MqttMsgBase struct {
	Action   string `json:"action"`
	Code     string `json:"code"`
	Msg      string `json:"msg"`
	Account  string `json:"acc"`
	AppId    string `json:"app_id"`
	MsgId    string `json:"msg_id"`
	DeviceId string `json:"device_id"`
	Content  string `json:"content"`
	Strict   bool   `json:"strict"`
}

func mD5EncodeToString(data string) string {
	h := md5.New()
	h.Write(str2Byte(data))
	x := h.Sum(nil)
	y := make([]byte, 32)
	hex.Encode(y, x)
	return byte2Str(y)
}

func str2Byte(s string) []byte {
	x := (*[2]uintptr)(unsafe.Pointer(&s))
	h := [3]uintptr{x[0], x[1], x[1]}
	return *(*[]byte)(unsafe.Pointer(&h))
}
func byte2Str(b []byte) string {
	return *(*string)(unsafe.Pointer(&b))
}
