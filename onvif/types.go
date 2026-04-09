package onvif

import "encoding/xml"

// ONVIF XML namespace constants used in SOAP envelopes and templates.
const (
	NsSoap = "http://www.w3.org/2003/05/soap-envelope"
	NsWSA  = "http://www.w3.org/2005/08/addressing"
	NsWSNT = "http://docs.oasis-open.org/wsn/b-2"
	NsTDS  = "http://www.onvif.org/ver10/device/wsdl"
	NsTRT  = "http://www.onvif.org/ver10/media/wsdl"
	NsTTR  = "http://www.onvif.org/ver10/schema"
	NsPTZ  = "http://www.onvif.org/ver20/ptz/wsdl"
	NsTEV  = "http://www.onvif.org/ver10/events/wsdl"
	NsTNS1 = "http://www.onvif.org/ver10/topics"
	NsXSD  = "http://www.w3.org/2001/XMLSchema"
	NsXSI  = "http://www.w3.org/2001/XMLSchema-instance"
)

// SOAP request parsing types — used to unmarshal incoming ONVIF requests.

type SOAPEnvelope struct {
	XMLName xml.Name    `xml:"Envelope"`
	Header  *SOAPHeader `xml:"Header"`
	Body    SOAPBody    `xml:"Body"`
}

type SOAPHeader struct {
	Security *WSSecurity `xml:"Security"`
}

type SOAPBody struct {
	Content []byte `xml:",innerxml"`
}

type WSSecurity struct {
	XMLName       xml.Name       `xml:"Security"`
	UsernameToken *UsernameToken `xml:"UsernameToken"`
}

type UsernameToken struct {
	Username string        `xml:"Username"`
	Password PasswordToken `xml:"Password"`
	Nonce    string        `xml:"Nonce"`
	Created  string        `xml:"Created"`
}

type PasswordToken struct {
	Type  string `xml:"Type,attr"`
	Value string `xml:",chardata"`
}

// Event notification types — used in the events subscription system.

type NotificationMessage struct {
	Topic   TopicExpression `xml:"Topic"`
	Message MessageContent  `xml:"Message"`
}

type TopicExpression struct {
	Dialect string `xml:"Dialect,attr"`
	Value   string `xml:",chardata"`
}

type MessageContent struct {
	UtcTime string      `xml:"UtcTime,attr"`
	Source  *SimpleItem `xml:"Source>SimpleItem,omitempty"`
	Data    *SimpleItem `xml:"Data>SimpleItem,omitempty"`
}

type SimpleItem struct {
	Name  string `xml:"Name,attr"`
	Value string `xml:"Value,attr"`
}
