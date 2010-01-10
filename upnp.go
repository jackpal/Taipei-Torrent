package main

// Just enough UPnP to be able to forward ports
//

import (
    "bytes"
    "http"
    "log"
    "os"
    "net"
    "strings"
    "strconv"
    "xml"
)

type upnpNAT struct {
    serviceURL string
}

type NAT interface {
    ForwardPort(protocol, externalPort, internalPort, description string, timeout int) (err os.Error)
    DeleteForwardingRule(protocol, externalPort string) (err os.Error)
}

func Discover() (nat NAT, err os.Error) {
    log.Stderr("Discover")
    ssdp, err := net.ResolveUDPAddr("239.255.255.250:1900")
    if err != nil {
        return
    }
    log.Stderr("DialUDP")
    socket, err := net.DialUDP("udp4", nil, nil)
    if err != nil {
        return
    }
    log.Stderr("SetReadTimeout")
    
    err = socket.SetReadTimeout(3 * 1000 * 1000 * 1000)
    if err != nil {
        return
    }
    
    buf := bytes.NewBufferString(
		"M-SEARCH * HTTP/1.1\r\n" +
		"HOST: 239.255.255.250:1900\r\n" +
		"ST:upnp:rootdevice\r\n" +
		"MAN:\"ssdp:discover\"\r\n" +
		"MX:3\r\n\r\n")
    message := buf.Bytes()
    log.Stderr("Write ", len(message))
    for i := 0; i < 3; i++ {
        _, err = socket.WriteToUDP(message, ssdp)
		if err != nil {
			return
		}
    }
    answerBytes := make([]byte, 1024)
    for i := 0; i < 10; i++ {
        log.Stderr("Read")
        var n int
    	n, _, err = socket.ReadFromUDP(answerBytes)
		if err != nil {
            socket.Close()
			return
		}
		answer := string(answerBytes[0:n])
        // log.Stderr("Answer: ", answer)
        if strings.Index(answer, "\r\nST: upnp:rootdevice\r\n") < 0 {
            continue
        }
        locString := "\r\nLocation: "
        locIndex := strings.Index(answer, locString)
        if locIndex < 0 {
            continue
        }
        loc := answer[locIndex + len(locString):]
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
        nat = &upnpNAT{serviceURL: serviceURL}
        break
    }
    socket.Close()
    return
}

type Service struct {
    ServiceType string
    ControlURL string
}

type DeviceList struct {
    Device []Device
}

type ServiceList struct {
    Service []Service
}

type Device struct {
    DeviceType string
    DeviceList DeviceList
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
	log.Stderr(root)
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
    a := rootURL[protoEndIndex + len(protocolEnd):]
    rootIndex := strings.Index(a, "/")
    return rootURL[0:protoEndIndex + len(protocolEnd) + rootIndex] + subURL
}

func soapRequest(url, function, message string) (r *http.Response, err os.Error) {
    fullMessage := "<?xml version=\"1.0\"?>" +
        "<s:Envelope xmlns:s=\"http://schemas.xmlsoap.org/soap/envelope/\" s:encodingStyle=\"http://schemas.xmlsoap.org/soap/encoding/\">\r\n" +
        "<s:Body>" + message + "</s:Body></s:Envelope>"
    
   //  body := bytes.NewBufferString(fullMessage)
    
	var req http.Request
	req.Method = "POST"
	// req.Body = body
	req.Body2 = fullMessage
	req.UserAgent = "Darwin/10.0.0, UPnP/1.0, MiniUPnPc/1.3"
	req.Header = map[string]string{
		"Content-Type": "text/xml",
		// "Transfer-Encoding": "chunked",
		"SOAPAction": "\"urn:schemas-upnp-org:service:WANIPConnection:1#" + function + "\"",
		"Connection": "Close",
		"Cache-Control": "no-cache",
		"Pragma": "no-cache",
	}

	req.URL, err = http.ParseURL(url)
	if err != nil {
		return
	}
	
	log.Stderr("soapRequest ", req)

	r, err = http.Send(&req)
	
	if r.StatusCode >= 4000 {
        err = os.NewError("Error " + strconv.Itoa(r.StatusCode) + " for " + function)
        r.Body.Close()
        r = nil
        return
    }
	return
}

func (n *upnpNAT) ForwardPort(protocol, externalPort, internalPort,
    description string, timeout int) (err os.Error) {
    message := "<u:AddPortMapping xmlns:u=\"urn:schemas-upnp-org:service:WANIPConnection:1\">\r\n" +
        "<NewRemoteHost></NewRemoteHost><NewExternalPort>" + externalPort +
        "</NewExternalPort><NewProtocol>" + protocol + "</NewProtocol>" +
        "<NewInternalPort>" + internalPort + "</NewInternalPort><NewInternalClient>" +
        "192.168.0.124" + // TODO: Put our IP address here.
        "</NewInternalClient><NewEnabled>1</NewEnabled><NewPortMappingDescription>" +
        description +
        "</NewPortMappingDescription><NewLeaseDuration>" + strconv.Itoa(timeout) + "</NewLeaseDuration></u:AddPortMapping>"
    
    var response *http.Response
    response, err = soapRequest(n.serviceURL, "AddPortMapping", message)

    log.Stderr("soap response: ", response)
    return
}

func (n *upnpNAT) DeleteForwardingRule(protocol, externalPort string) (err os.Error) {
    return
}

func testUPnP() {
    log.Stderr("Starting UPnP test")
    nat, err := Discover()
    log.Stderr("nat ", nat, "err ", err)
    if err != nil {
        return
    }
    port := "60001"
    err = nat.ForwardPort("TCP", port, port, "Taipei-Torrent", 0)
    log.Stderr("err ", err)
    err = nat.DeleteForwardingRule("TCP", port)
    log.Stderr("err ", err)
}
