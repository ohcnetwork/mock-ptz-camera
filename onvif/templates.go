package onvif

import (
	"bytes"
	"text/template"
)

// Template data types

type envelopeData struct {
	Body string
}

type faultData struct {
	Code   string
	Reason string
}

type dateTimeData struct {
	Hour, Minute, Second int
	Year, Month, Day     int
}

type serviceURLsData struct {
	DeviceURL string
	MediaURL  string
	PTZURL    string
	EventsURL string
}

type mediaConfigData struct {
	Width  int
	Height int
	FPS    int
}

type streamURIData struct {
	URI string
}

type ptzStatusData struct {
	Pan, Tilt, Zoom           float64
	PanTiltStatus, ZoomStatus string
}

type presetData struct {
	Token           string
	Name            string
	Pan, Tilt, Zoom float64
}

type presetTokenData struct {
	Token string
}

type subscriptionData struct {
	Address         string
	CurrentTime     string
	TerminationTime string
}

type notificationData struct {
	Dialect     string
	Topic       string
	UtcTime     string
	SourceName  string
	SourceValue string
	DataName    string
	DataValue   string
}

type pullMessagesData struct {
	CurrentTime     string
	TerminationTime string
	Messages        string
}

type renewData struct {
	TerminationTime string
	CurrentTime     string
}

type probeMatchData struct {
	MessageID string
	RelatesTo string
	DeviceURL string
}

// Templates

var onvifTemplates = template.New("onvif")

func init() {
	t := func(name, text string) {
		template.Must(onvifTemplates.New(name).Parse(text))
	}

	// SOAP envelope wrapper (data: envelopeData)
	t("soapEnvelope", `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope
	xmlns:s="`+NsSoap+`"
	xmlns:tt="`+NsTTR+`"
	xmlns:tds="`+NsTDS+`"
	xmlns:trt="`+NsTRT+`"
	xmlns:tptz="`+NsPTZ+`"
	xmlns:tev="`+NsTEV+`"
	xmlns:wsnt="`+NsWSNT+`"
	xmlns:xsi="`+NsXSI+`"
	xmlns:xsd="`+NsXSD+`">
	<s:Body>
		{{.Body}}
	</s:Body>
</s:Envelope>`)

	// SOAP fault (data: faultData)
	t("soapFault", `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope xmlns:s="`+NsSoap+`">
	<s:Body>
		<s:Fault>
			<s:Code>
				<s:Value>{{.Code}}</s:Value>
			</s:Code>
			<s:Reason>
				<s:Text xml:lang="en">{{.Reason}}</s:Text>
			</s:Reason>
		</s:Fault>
	</s:Body>
</s:Envelope>`)

	// Device service: GetSystemDateAndTime (data: dateTimeData)
	t("getSystemDateAndTime", `<tds:GetSystemDateAndTimeResponse>
	<tds:SystemDateAndTime>
		<tt:DateTimeType>NTP</tt:DateTimeType>
		<tt:DaylightSavings>false</tt:DaylightSavings>
		<tt:UTCDateTime>
			<tt:Time>
				<tt:Hour>{{.Hour}}</tt:Hour>
				<tt:Minute>{{.Minute}}</tt:Minute>
				<tt:Second>{{.Second}}</tt:Second>
			</tt:Time>
			<tt:Date>
				<tt:Year>{{.Year}}</tt:Year>
				<tt:Month>{{.Month}}</tt:Month>
				<tt:Day>{{.Day}}</tt:Day>
			</tt:Date>
		</tt:UTCDateTime>
	</tds:SystemDateAndTime>
</tds:GetSystemDateAndTimeResponse>`)

	// Device service: GetDeviceInformation (data: nil)
	t("getDeviceInformation", `<tds:GetDeviceInformationResponse>
	<tds:Manufacturer>OHCNetwork</tds:Manufacturer>
	<tds:Model>MockPTZ-360</tds:Model>
	<tds:FirmwareVersion>1.0.0</tds:FirmwareVersion>
	<tds:SerialNumber>MOCK-PTZ-001</tds:SerialNumber>
	<tds:HardwareId>MockHW-1.0</tds:HardwareId>
</tds:GetDeviceInformationResponse>`)

	// Device service: GetServices (data: serviceURLsData)
	t("getServices", `<tds:GetServicesResponse>
	<tds:Service>
		<tds:Namespace>http://www.onvif.org/ver10/device/wsdl</tds:Namespace>
		<tds:XAddr>{{.DeviceURL}}</tds:XAddr>
		<tds:Version><tt:Major>2</tt:Major><tt:Minor>0</tt:Minor></tds:Version>
	</tds:Service>
	<tds:Service>
		<tds:Namespace>http://www.onvif.org/ver10/media/wsdl</tds:Namespace>
		<tds:XAddr>{{.MediaURL}}</tds:XAddr>
		<tds:Version><tt:Major>2</tt:Major><tt:Minor>0</tt:Minor></tds:Version>
	</tds:Service>
	<tds:Service>
		<tds:Namespace>http://www.onvif.org/ver20/ptz/wsdl</tds:Namespace>
		<tds:XAddr>{{.PTZURL}}</tds:XAddr>
		<tds:Version><tt:Major>2</tt:Major><tt:Minor>0</tt:Minor></tds:Version>
	</tds:Service>
	<tds:Service>
		<tds:Namespace>http://www.onvif.org/ver10/events/wsdl</tds:Namespace>
		<tds:XAddr>{{.EventsURL}}</tds:XAddr>
		<tds:Version><tt:Major>2</tt:Major><tt:Minor>0</tt:Minor></tds:Version>
	</tds:Service>
</tds:GetServicesResponse>`)

	// Device service: GetCapabilities (data: serviceURLsData)
	t("getCapabilities", `<tds:GetCapabilitiesResponse>
	<tds:Capabilities>
		<tt:Device><tt:XAddr>{{.DeviceURL}}</tt:XAddr></tt:Device>
		<tt:Media><tt:XAddr>{{.MediaURL}}</tt:XAddr></tt:Media>
		<tt:PTZ><tt:XAddr>{{.PTZURL}}</tt:XAddr></tt:PTZ>
		<tt:Events><tt:XAddr>{{.EventsURL}}</tt:XAddr></tt:Events>
	</tds:Capabilities>
</tds:GetCapabilitiesResponse>`)

	// Device service: GetScopes (data: nil)
	t("getScopes", `<tds:GetScopesResponse>
	<tds:Scopes>
		<tt:ScopeDef>Fixed</tt:ScopeDef>
		<tt:ScopeItem>onvif://www.onvif.org/type/Network_Video_Transmitter</tt:ScopeItem>
	</tds:Scopes>
	<tds:Scopes>
		<tt:ScopeDef>Fixed</tt:ScopeDef>
		<tt:ScopeItem>onvif://www.onvif.org/name/MockPTZ</tt:ScopeItem>
	</tds:Scopes>
	<tds:Scopes>
		<tt:ScopeDef>Fixed</tt:ScopeDef>
		<tt:ScopeItem>onvif://www.onvif.org/hardware/MockHW</tt:ScopeItem>
	</tds:Scopes>
</tds:GetScopesResponse>`)

	// Shared sub-template: video encoder config body (data: mediaConfigData)
	t("videoEncoderConfigBody", `<tt:Name>VideoEncoder_1</tt:Name>
<tt:Encoding>H264</tt:Encoding>
<tt:Resolution>
	<tt:Width>{{.Width}}</tt:Width>
	<tt:Height>{{.Height}}</tt:Height>
</tt:Resolution>
<tt:RateControl>
	<tt:FrameRateLimit>{{.FPS}}</tt:FrameRateLimit>
	<tt:EncodingInterval>1</tt:EncodingInterval>
	<tt:BitrateLimit>2000</tt:BitrateLimit>
</tt:RateControl>
<tt:Quality>5</tt:Quality>
<tt:H264>
	<tt:GovLength>{{.FPS}}</tt:GovLength>
	<tt:H264Profile>Baseline</tt:H264Profile>
</tt:H264>`)

	// Media service: GetProfiles (data: mediaConfigData)
	t("getProfiles", `<trt:GetProfilesResponse>
	<trt:Profiles token="MainProfile" fixed="true">
		<tt:Name>MainProfile</tt:Name>
		<tt:VideoSourceConfiguration token="VSC_1">
			<tt:Name>VideoSource_1</tt:Name>
			<tt:SourceToken>VS_1</tt:SourceToken>
			<tt:Bounds x="0" y="0" width="{{.Width}}" height="{{.Height}}"/>
		</tt:VideoSourceConfiguration>
		<tt:VideoEncoderConfiguration token="VEC_1">
			{{template "videoEncoderConfigBody" .}}
		</tt:VideoEncoderConfiguration>
		<tt:PTZConfiguration token="PTZ_1">
			<tt:Name>PTZ_Config</tt:Name>
			<tt:NodeToken>PTZNode_1</tt:NodeToken>
			<tt:DefaultContinuousPanTiltVelocitySpace>http://www.onvif.org/ver10/tptz/PanTiltSpaces/VelocityGenericSpace</tt:DefaultContinuousPanTiltVelocitySpace>
			<tt:DefaultContinuousZoomVelocitySpace>http://www.onvif.org/ver10/tptz/ZoomSpaces/VelocityGenericSpace</tt:DefaultContinuousZoomVelocitySpace>
			<tt:DefaultPTZTimeout>PT10S</tt:DefaultPTZTimeout>
		</tt:PTZConfiguration>
	</trt:Profiles>
</trt:GetProfilesResponse>`)

	// Media service: GetStreamUri (data: streamURIData)
	t("getStreamUri", `<trt:GetStreamUriResponse>
	<trt:MediaUri>
		<tt:Uri>{{.URI}}</tt:Uri>
		<tt:InvalidAfterConnect>false</tt:InvalidAfterConnect>
		<tt:InvalidAfterReboot>false</tt:InvalidAfterReboot>
		<tt:Timeout>PT60S</tt:Timeout>
	</trt:MediaUri>
</trt:GetStreamUriResponse>`)

	// Media service: GetVideoSources (data: mediaConfigData)
	t("getVideoSources", `<trt:GetVideoSourcesResponse>
	<trt:VideoSources token="VS_1">
		<tt:Resolution>
			<tt:Width>{{.Width}}</tt:Width>
			<tt:Height>{{.Height}}</tt:Height>
		</tt:Resolution>
		<tt:Framerate>{{.FPS}}</tt:Framerate>
	</trt:VideoSources>
</trt:GetVideoSourcesResponse>`)

	// Media service: GetVideoSourceConfigurations (data: mediaConfigData)
	t("getVideoSourceConfigurations", `<trt:GetVideoSourceConfigurationsResponse>
	<trt:Configurations token="VSC_1">
		<tt:Name>VideoSource_1</tt:Name>
		<tt:SourceToken>VS_1</tt:SourceToken>
		<tt:Bounds x="0" y="0" width="{{.Width}}" height="{{.Height}}"/>
	</trt:Configurations>
</trt:GetVideoSourceConfigurationsResponse>`)

	// Media service: GetVideoEncoderConfigurations (data: mediaConfigData)
	t("getVideoEncoderConfigurations", `<trt:GetVideoEncoderConfigurationsResponse>
	<trt:Configurations token="VEC_1">
		{{template "videoEncoderConfigBody" .}}
	</trt:Configurations>
</trt:GetVideoEncoderConfigurationsResponse>`)

	// PTZ service: GetStatus (data: ptzStatusData)
	t("getStatus", `<tptz:GetStatusResponse>
	<tptz:PTZStatus>
		<tt:Position>
			<tt:PanTilt x="{{printf "%.4f" .Pan}}" y="{{printf "%.4f" .Tilt}}" space="http://www.onvif.org/ver10/tptz/PanTiltSpaces/PositionGenericSpace"/>
			<tt:Zoom x="{{printf "%.4f" .Zoom}}" space="http://www.onvif.org/ver10/tptz/ZoomSpaces/PositionGenericSpace"/>
		</tt:Position>
		<tt:MoveStatus>
			<tt:PanTilt>{{.PanTiltStatus}}</tt:PanTilt>
			<tt:Zoom>{{.ZoomStatus}}</tt:Zoom>
		</tt:MoveStatus>
	</tptz:PTZStatus>
</tptz:GetStatusResponse>`)

	// PTZ service: individual preset entry (data: presetData)
	t("ptzPreset", `<tptz:Preset token="{{.Token}}">
	<tt:Name>{{.Name}}</tt:Name>
	<tt:PTZPosition>
		<tt:PanTilt x="{{printf "%.4f" .Pan}}" y="{{printf "%.4f" .Tilt}}"/>
		<tt:Zoom x="{{printf "%.4f" .Zoom}}"/>
	</tt:PTZPosition>
</tptz:Preset>`)

	// PTZ service: SetPresetResponse (data: presetTokenData)
	t("setPresetResponse", `<tptz:SetPresetResponse>
	<tptz:PresetToken>{{.Token}}</tptz:PresetToken>
</tptz:SetPresetResponse>`)

	// PTZ service: GetNodes (data: nil)
	t("getNodes", `<tptz:GetNodesResponse>
	<tptz:PTZNode token="PTZNode_1" FixedHomePosition="false">
		<tt:Name>MockPTZNode</tt:Name>
		<tt:SupportedPTZSpaces>
			<tt:AbsolutePanTiltPositionSpace>
				<tt:URI>http://www.onvif.org/ver10/tptz/PanTiltSpaces/PositionGenericSpace</tt:URI>
				<tt:XRange><tt:Min>-1</tt:Min><tt:Max>1</tt:Max></tt:XRange>
				<tt:YRange><tt:Min>-0.33</tt:Min><tt:Max>1</tt:Max></tt:YRange>
			</tt:AbsolutePanTiltPositionSpace>
			<tt:AbsoluteZoomPositionSpace>
				<tt:URI>http://www.onvif.org/ver10/tptz/ZoomSpaces/PositionGenericSpace</tt:URI>
				<tt:XRange><tt:Min>0</tt:Min><tt:Max>1</tt:Max></tt:XRange>
			</tt:AbsoluteZoomPositionSpace>
			<tt:RelativePanTiltTranslationSpace>
				<tt:URI>http://www.onvif.org/ver10/tptz/PanTiltSpaces/TranslationGenericSpace</tt:URI>
				<tt:XRange><tt:Min>-1</tt:Min><tt:Max>1</tt:Max></tt:XRange>
				<tt:YRange><tt:Min>-1</tt:Min><tt:Max>1</tt:Max></tt:YRange>
			</tt:RelativePanTiltTranslationSpace>
			<tt:RelativeZoomTranslationSpace>
				<tt:URI>http://www.onvif.org/ver10/tptz/ZoomSpaces/TranslationGenericSpace</tt:URI>
				<tt:XRange><tt:Min>-1</tt:Min><tt:Max>1</tt:Max></tt:XRange>
			</tt:RelativeZoomTranslationSpace>
			<tt:ContinuousPanTiltVelocitySpace>
				<tt:URI>http://www.onvif.org/ver10/tptz/PanTiltSpaces/VelocityGenericSpace</tt:URI>
				<tt:XRange><tt:Min>-1</tt:Min><tt:Max>1</tt:Max></tt:XRange>
				<tt:YRange><tt:Min>-1</tt:Min><tt:Max>1</tt:Max></tt:YRange>
			</tt:ContinuousPanTiltVelocitySpace>
			<tt:ContinuousZoomVelocitySpace>
				<tt:URI>http://www.onvif.org/ver10/tptz/ZoomSpaces/VelocityGenericSpace</tt:URI>
				<tt:XRange><tt:Min>-1</tt:Min><tt:Max>1</tt:Max></tt:XRange>
			</tt:ContinuousZoomVelocitySpace>
		</tt:SupportedPTZSpaces>
		<tt:MaximumNumberOfPresets>50</tt:MaximumNumberOfPresets>
		<tt:HomeSupported>false</tt:HomeSupported>
	</tptz:PTZNode>
</tptz:GetNodesResponse>`)

	// PTZ service: GetConfigurations (data: nil)
	t("getConfigurations", `<tptz:GetConfigurationsResponse>
	<tptz:PTZConfiguration token="PTZ_1">
		<tt:Name>PTZ_Config</tt:Name>
		<tt:NodeToken>PTZNode_1</tt:NodeToken>
		<tt:DefaultAbsolutePantTiltPositionSpace>http://www.onvif.org/ver10/tptz/PanTiltSpaces/PositionGenericSpace</tt:DefaultAbsolutePantTiltPositionSpace>
		<tt:DefaultAbsoluteZoomPositionSpace>http://www.onvif.org/ver10/tptz/ZoomSpaces/PositionGenericSpace</tt:DefaultAbsoluteZoomPositionSpace>
		<tt:DefaultRelativePanTiltTranslationSpace>http://www.onvif.org/ver10/tptz/PanTiltSpaces/TranslationGenericSpace</tt:DefaultRelativePanTiltTranslationSpace>
		<tt:DefaultRelativeZoomTranslationSpace>http://www.onvif.org/ver10/tptz/ZoomSpaces/TranslationGenericSpace</tt:DefaultRelativeZoomTranslationSpace>
		<tt:DefaultContinuousPanTiltVelocitySpace>http://www.onvif.org/ver10/tptz/PanTiltSpaces/VelocityGenericSpace</tt:DefaultContinuousPanTiltVelocitySpace>
		<tt:DefaultContinuousZoomVelocitySpace>http://www.onvif.org/ver10/tptz/ZoomSpaces/VelocityGenericSpace</tt:DefaultContinuousZoomVelocitySpace>
		<tt:DefaultPTZTimeout>PT10S</tt:DefaultPTZTimeout>
	</tptz:PTZConfiguration>
</tptz:GetConfigurationsResponse>`)

	// Events service: GetEventProperties (data: nil)
	t("getEventProperties", `<tev:GetEventPropertiesResponse>
	<tev:TopicNamespaceLocation>http://www.onvif.org/ver10/topics/topicns.xml</tev:TopicNamespaceLocation>
	<wsnt:FixedTopicSet>true</wsnt:FixedTopicSet>
	<wstop:TopicSet xmlns:wstop="http://docs.oasis-open.org/wsn/t-1" xmlns:tns1="`+NsTNS1+`">
		<tns1:PTZ>
			<tns1:Position>
				<tns1:Changed wstop:topic="true">
					<tt:MessageDescription IsProperty="false">
						<tt:Source><tt:SimpleItemDescription Name="ProfileToken" Type="xsd:string"/></tt:Source>
						<tt:Data><tt:SimpleItemDescription Name="Position" Type="xsd:string"/></tt:Data>
					</tt:MessageDescription>
				</tns1:Changed>
			</tns1:Position>
		</tns1:PTZ>
	</wstop:TopicSet>
	<wsnt:TopicExpressionDialect>http://www.onvif.org/ver10/tev/topicExpression/ConcreteSet</wsnt:TopicExpressionDialect>
	<tev:MessageContentFilterDialect>http://www.onvif.org/ver10/tev/messageContentFilter/ItemFilter</tev:MessageContentFilterDialect>
</tev:GetEventPropertiesResponse>`)

	// Events service: CreatePullPointSubscription (data: subscriptionData)
	t("createPullPointSubscription", `<tev:CreatePullPointSubscriptionResponse>
	<tev:SubscriptionReference>
		<wsa:Address xmlns:wsa="`+NsWSA+`">{{.Address}}</wsa:Address>
	</tev:SubscriptionReference>
	<wsnt:CurrentTime>{{.CurrentTime}}</wsnt:CurrentTime>
	<wsnt:TerminationTime>{{.TerminationTime}}</wsnt:TerminationTime>
</tev:CreatePullPointSubscriptionResponse>`)

	// Events service: individual notification message (data: notificationData)
	t("notificationMessage", `<wsnt:NotificationMessage>
	<wsnt:Topic Dialect="{{.Dialect}}">{{.Topic}}</wsnt:Topic>
	<wsnt:Message>
		<tt:Message UtcTime="{{.UtcTime}}">
			<tt:Source><tt:SimpleItem Name="{{.SourceName}}" Value="{{.SourceValue}}"/></tt:Source>
			<tt:Data><tt:SimpleItem Name="{{.DataName}}" Value="{{.DataValue}}"/></tt:Data>
		</tt:Message>
	</wsnt:Message>
</wsnt:NotificationMessage>`)

	// Events service: PullMessagesResponse (data: pullMessagesData)
	t("pullMessagesResponse", `<tev:PullMessagesResponse>
	<tev:CurrentTime>{{.CurrentTime}}</tev:CurrentTime>
	<tev:TerminationTime>{{.TerminationTime}}</tev:TerminationTime>
	{{.Messages}}
</tev:PullMessagesResponse>`)

	// Events service: RenewResponse (data: renewData)
	t("renewResponse", `<wsnt:RenewResponse>
	<wsnt:TerminationTime>{{.TerminationTime}}</wsnt:TerminationTime>
	<wsnt:CurrentTime>{{.CurrentTime}}</wsnt:CurrentTime>
</wsnt:RenewResponse>`)

	// WS-Discovery: ProbeMatch (data: probeMatchData)
	t("probeMatch", `<?xml version="1.0" encoding="UTF-8"?>
<s:Envelope
	xmlns:s="http://www.w3.org/2003/05/soap-envelope"
	xmlns:a="http://schemas.xmlsoap.org/ws/2004/08/addressing"
	xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery">
	<s:Header>
		<a:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches</a:Action>
		<a:MessageID>{{.MessageID}}</a:MessageID>
		<a:RelatesTo>{{.RelatesTo}}</a:RelatesTo>
		<a:To>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</a:To>
	</s:Header>
	<s:Body>
		<d:ProbeMatches>
			<d:ProbeMatch>
				<a:EndpointReference>
					<a:Address>urn:uuid:mock-ptz-camera-001</a:Address>
				</a:EndpointReference>
				<d:Types>dn:NetworkVideoTransmitter</d:Types>
				<d:Scopes>onvif://www.onvif.org/type/Network_Video_Transmitter onvif://www.onvif.org/name/MockPTZ</d:Scopes>
				<d:XAddrs>{{.DeviceURL}}</d:XAddrs>
				<d:MetadataVersion>1</d:MetadataVersion>
			</d:ProbeMatch>
		</d:ProbeMatches>
	</s:Body>
</s:Envelope>`)
}

// renderTemplate executes a named template and returns the result as a string.
func renderTemplate(name string, data interface{}) string {
	var buf bytes.Buffer
	if err := onvifTemplates.ExecuteTemplate(&buf, name, data); err != nil {
		return ""
	}
	return buf.String()
}
