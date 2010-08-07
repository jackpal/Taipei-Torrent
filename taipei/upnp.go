package taipei

// Just enough UPnP to be able to forward ports
//

import (
	"bytes"
	"jackpal/http"
	"os"
	"net"
	"strings"
	"strconv"
	"xml"
)

type upnpNAT struct {
	serviceURL string
	ourIP      string
}

type NAT interface {
	AddPortMapping(protocol string, externalPort, internalPort int, description string, timeout int) (err os.Error)
	DeletePortMapping(protocol string, externalPort int) (err os.Error)
}

func Discover() (nat NAT, err os.Error) {
	ssdp, err := net.ResolveUDPAddr("239.255.255.250:1900")
	if err != nil {
		return
	}
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return
	}
	socket := conn.(*net.UDPConn)
	defer socket.Close()

	err = socket.SetReadTimeout(3 * 1000 * 1000 * 1000)
	if err != nil {
		return
	}

	st := "ST: urn:schemas-upnp-org:device:InternetGatewayDevice:1\r\n"
	buf := bytes.NewBufferString(
		"M-SEARCH * HTTP/1.1\r\n" +
			"HOST: 239.255.255.250:1900\r\n" +
			st +
			"MAN: \"ssdp:discover\"\r\n" +
			"MX: 2\r\n\r\n")
	message := buf.Bytes()
	answerBytes := make([]byte, 1024)
	for i := 0; i < 3; i++ {
		_, err = socket.WriteToUDP(message, ssdp)
		if err != nil {
			return
		}
		var n int
		n, _, err = socket.ReadFromUDP(answerBytes)
		if err != nil {
			continue
			// socket.Close()
			// return
		}
		answer := string(answerBytes[0:n])
		if strings.Index(answer, "\r\n"+st) < 0 {
			continue
		}
		// HTTP header field names are case-insensitive.
		// http://www.w3.org/Protocols/rfc2616/rfc2616-sec4.html#sec4.2
		locString := "\r\nlocation: "
		answer = strings.ToLower(answer)
		locIndex := strings.Index(answer, locString)
		if locIndex < 0 {
			continue
		}
		loc := answer[locIndex+len(locString):]
		endIndex := strings.Index(loc, "\r\n")
		if endIndex < 0 {
			continue
		}
		locURL := loc[0:endIndex]
		var serviceURL string
		serviceURL, err = getServiceURL(locURL)
		if err != nil {
			return
		}
		var ourIP string
		ourIP, err = getOurIP()
		if err != nil {
			return
		}
		nat = &upnpNAT{serviceURL: serviceURL, ourIP: ourIP}
		return
	}
	err = os.NewError("UPnP port discovery failed.")
	return
}

type Service struct {
	ServiceType string
	ControlURL  string
}

type DeviceList struct {
	Device []Device
}

type ServiceList struct {
	Service []Service
}

type Device struct {
	DeviceType  string
	DeviceList  DeviceList
	ServiceList ServiceList
}

type Root struct {
	Device Device
}

func getChildDevice(d *Device, deviceType string) *Device {
	dl := d.DeviceList.Device
	for i := 0; i < len(dl); i++ {
		if dl[i].DeviceType == deviceType {
			return &dl[i]
		}
	}
	return nil
}

func getChildService(d *Device, serviceType string) *Service {
	sl := d.ServiceList.Service
	for i := 0; i < len(sl); i++ {
		if sl[i].ServiceType == serviceType {
			return &sl[i]
		}
	}
	return nil
}


func getOurIP() (ip string, err os.Error) {
	hostname, err := os.Hostname()
	if err != nil {
		return
	}
	_, addrs, err := net.LookupHost(hostname)
	if err != nil {
		return
	}
	if len(addrs) < 1 {
		err = os.NewError("No addresses.")
		return
	}
	ip = addrs[0]
	return
}

func getServiceURL(rootURL string) (url string, err os.Error) {
	r, _, err := http.Get(rootURL)
	if err != nil {
		return
	}
	defer r.Body.Close()
	if r.StatusCode >= 400 {
		err = os.NewError(string(r.StatusCode))
		return
	}
	var root Root
	err = xml.Unmarshal(r.Body, &root)
	if err != nil {
		return
	}
	a := &root.Device
	if a.DeviceType != "urn:schemas-upnp-org:device:InternetGatewayDevice:1" {
		err = os.NewError("No InternetGatewayDevice")
		return
	}
	b := getChildDevice(a, "urn:schemas-upnp-org:device:WANDevice:1")
	if b == nil {
		err = os.NewError("No WANDevice")
		return
	}
	c := getChildDevice(b, "urn:schemas-upnp-org:device:WANConnectionDevice:1")
	if c == nil {
		err = os.NewError("No WANConnectionDevice")
		return
	}
	d := getChildService(c, "urn:schemas-upnp-org:service:WANIPConnection:1")
	if d == nil {
		err = os.NewError("No WANIPConnection")
		return
	}
	url = combineURL(rootURL, d.ControlURL)
	return
}

func combineURL(rootURL, subURL string) string {
	protocolEnd := "://"
	protoEndIndex := strings.Index(rootURL, protocolEnd)
	a := rootURL[protoEndIndex+len(protocolEnd):]
	rootIndex := strings.Index(a, "/")
	return rootURL[0:protoEndIndex+len(protocolEnd)+rootIndex] + subURL
}

type stringBuffer struct {
	base    string
	current string
}

func NewStringBuffer(s string) *stringBuffer { return &stringBuffer{s, s} }

func (sb *stringBuffer) Read(p []byte) (n int, err os.Error) {
	s := sb.current
	lenStr := len(s)
	if lenStr == 0 {
		return 0, os.EOF
	}
	n = len(p)
	if n > lenStr {
		n = lenStr
		err = os.EOF
	}
	for i := 0; i < n; i++ {
		p[i] = s[i]
	}
	sb.current = s[n:]
	return
}

func (sb *stringBuffer) Seek(offset int64, whence int) (ret int64, err os.Error) {
	var newOffset int64
	switch whence {
	case 0: // from beginning
		newOffset = offset
	case 1: // relative
		newOffset = int64(len(sb.base)-len(sb.current)) + offset
	case 2: // from end
		newOffset = int64(len(sb.base)) - offset
	default:
		err = os.NewError("bad whence")
		return
	}
	if newOffset < 0 || newOffset > int64(len(sb.base)) {
		err = os.NewError("offset out of range")
	} else {
		sb.current = sb.base[newOffset:]
		ret = newOffset
	}
	return
}

func soapRequest(url, function, message string) (r *http.Response, err os.Error) {
	fullMessage := "<?xml version=\"1.0\" ?>" +
		"<s:Envelope xmlns:s=\"http://schemas.xmlsoap.org/soap/envelope/\" s:encodingStyle=\"http://schemas.xmlsoap.org/soap/encoding/\">\r\n" +
		"<s:Body>" + message + "</s:Body></s:Envelope>"

	var req http.Request
	req.Method = "POST"
	req.Body = NewStringBuffer(fullMessage)
	req.UserAgent = "Darwin/10.0.0, UPnP/1.0, MiniUPnPc/1.3"
	req.Header = map[string]string{
		"Content-Type": "text/xml ; charset=\"utf-8\"",
		// "Transfer-Encoding": "chunked",
		"SOAPAction":    "\"urn:schemas-upnp-org:service:WANIPConnection:1#" + function + "\"",
		"Connection":    "Close",
		"Cache-Control": "no-cache",
		"Pragma":        "no-cache",
	}

	req.URL, err = http.ParseURL(url)
	if err != nil {
		return
	}

	// log.Stderr("soapRequest ", req)

	r, err = http.Send(&req)

	if r.StatusCode >= 400 {
		// log.Stderr(function, r.StatusCode)
		err = os.NewError("Error " + strconv.Itoa(r.StatusCode) + " for " + function)
		r.Body.Close()
		r = nil
		return
	}
	return
}

func (n *upnpNAT) GetStatusInfo() (err os.Error) {

	message := "<u:GetStatusInfo xmlns:u=\"urn:schemas-upnp-org:service:WANIPConnection:1\">\r\n" +
		"</u:GetStatusInfo>"

	var response *http.Response
	response, err = soapRequest(n.serviceURL, "GetStatusInfo", message)
	if err != nil {
		return
	}

	// TODO: Write a soap reply parser. It has to eat the Body and envelope tags...

	response.Body.Close()
	return
}


func (n *upnpNAT) AddPortMapping(protocol string, externalPort, internalPort int, description string, timeout int) (err os.Error) {
	// A single concatenation would brake ARM compilation.
	message := "<u:AddPortMapping xmlns:u=\"urn:schemas-upnp-org:service:WANIPConnection:1\">\r\n" +
		"<NewRemoteHost></NewRemoteHost><NewExternalPort>" + strconv.Itoa(externalPort)
	message += "</NewExternalPort><NewProtocol>" + protocol + "</NewProtocol>"
	message += "<NewInternalPort>" + strconv.Itoa(internalPort) + "</NewInternalPort>" +
		"<NewInternalClient>" + n.ourIP + "</NewInternalClient>" +
		"<NewEnabled>1</NewEnabled><NewPortMappingDescription>"
	message += description +
		"</NewPortMappingDescription><NewLeaseDuration>" + strconv.Itoa(timeout) +
		"</NewLeaseDuration></u:AddPortMapping>"

	var response *http.Response
	response, err = soapRequest(n.serviceURL, "AddPortMapping", message)
	if err != nil {
		return
	}

	// TODO: check response to see if the port was forwarded
	// log.Stderr(message, response)
	_ = response
	return
}


func (n *upnpNAT) DeletePortMapping(protocol string, externalPort int) (err os.Error) {

	message := "<u:DeletePortMapping xmlns:u=\"urn:schemas-upnp-org:service:WANIPConnection:1\">\r\n" +
		"<NewRemoteHost></NewRemoteHost><NewExternalPort>" + strconv.Itoa(externalPort) +
		"</NewExternalPort><NewProtocol>" + protocol + "</NewProtocol>" +
		"</u:DeletePortMapping>"

	var response *http.Response
	response, err = soapRequest(n.serviceURL, "DeletePortMapping", message)
	if err != nil {
		return
	}

	// TODO: check response to see if the port was deleted
	// log.Stderr(message, response)
	_ = response
	return
}
