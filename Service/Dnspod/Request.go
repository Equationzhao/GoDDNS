package Dnspod

import (
	"GodDns/Core"
	log "GodDns/Log"
	"GodDns/Net"
	json "GodDns/Util/Json"
	"errors"
	"fmt"
	"github.com/go-resty/resty/v2"
	"net/url"
	"strconv"
	"time"
)

const (
	// RecordListUrl url of getting Record list
	RecordListUrl = "https://dnsapi.cn/Record.List"
	// DDNSURL  url of DDNS
	DDNSURL = "https://dnsapi.cn/Record.Ddns"
)

type empty struct{}

// usage
// r:=Dnspod.Request
// r.Init(Parameters)
// r.MakeRequest()

// Request implements DDNS.Request
type Request struct {
	parameters Parameters
	status     Core.Status
}

// Target return target domain
func (r *Request) Target() string {
	return r.parameters.Subdomain + "." + r.parameters.Domain
}

// Status return DDNS.Status which contains execution result etc.
func (r *Request) Status() Core.Status {
	return r.status
}

func newStatus() *Core.Status {
	return &Core.Status{
		Name:   serviceName,
		Status: Core.NotExecute,
		MG:     Core.NewDefaultMsgGroup(),
	}
}

// ToParameters return DDNS.Parameters
func (r *Request) ToParameters() Core.Service {
	return &r.parameters
}

// GetName return "dnspod"
func (r *Request) GetName() string {
	return serviceName
}

// Init set parameter
func (r *Request) Init(parameters Parameters) error {
	r.parameters = parameters

	return nil
}

func (r *Request) encodeURLWithoutIDContent() url.Values {
	v := url.Values{}
	v.Add("login_token", r.parameters.LoginToken)
	v.Add("format", r.parameters.Format)
	v.Add("lang", r.parameters.Lang)
	v.Add("error_on_empty", r.parameters.ErrorOnEmpty)
	v.Add("domain", r.parameters.Domain)
	v.Add("sub_domain", r.parameters.Subdomain)
	v.Add("record_line", r.parameters.RecordLine)

	v.Add("record_type", r.parameters.Type)
	return v
}

func (r *Request) encodeURLWithoutID() (content string) {
	content = r.encodeURLWithoutIDContent().Encode()
	return content
}

func (r *Request) encodeURL() (content string) {
	v := r.encodeURLWithoutIDContent()
	ttl := strconv.Itoa(int(r.parameters.TTL))
	v.Add("ttl", ttl)
	v.Add("value", r.parameters.Value)
	id := strconv.Itoa(int(r.parameters.RecordId))
	v.Add("record_id", id)
	content = v.Encode()
	return content
}

func (r *Request) RequestThroughProxy() error {

	done := make(chan empty)
	status := newStatus()
	var err error
	_ = Core.MainGoroutinePool.Submit(func() {
		*status, err = r.GetRecordIdByProxy()
		done <- empty{}
	})

	s := &resOfddns{}

	content := ""
	select {
	case <-done:
		if err != nil || status.Status != Core.Success {
			r.status.Name = serviceName
			r.status.Status = Core.Failed
			for _, i := range status.MG.GetInfo() {
				r.status.MG.AddInfo(i.String())
			}

			for _, i := range status.MG.GetWarn() {
				r.status.MG.AddWarn(i.String())
			}

			for _, i := range status.MG.GetError() {
				r.status.MG.AddError(i.String())
			}

			r.status.MG.AddError(err.Error())
			return err
		}
		// content = Util.Convert2XWWWFormUrlencoded(&r.parameters)
		content = r.encodeURL()

	case <-time.After(time.Second * 20):
		r.status.Status = Core.Timeout
		r.status.MG.AddError("GetRecordId timeout")
		return errors.New("GetRecordId timeout")
	}

	log.Debugf("content:%s", content)

	iter := Net.GlobalProxies.GetProxyIter()
	client := Core.MainClientPool.Get().(*resty.Client)
	defer Core.MainClientPool.Put(client)
	req := client.R()
	for iter.NotLast() {
		proxy := iter.Next()
		response, err := req.SetResult(s).SetHeader("Content-Type", "application/x-www-form-urlencoded").SetBody([]byte(content)).Post(DDNSURL)
		if err != nil {
			errMsg := fmt.Sprintf("request error through proxy %s: %v", proxy, err)
			r.status.MG.AddError(errMsg)
			log.Errorf(errMsg)
			continue
		} else {
			log.Debugf("result:%+v", string(response.Body()))
			_ = json.Unmarshal(response.Body(), s)
			log.Debugf("after marshall:%+v", s)
			break
		}
	}
	r.status = *code2status(s.Status.Code)
	resultMsg := fmt.Sprintf("%s at %s %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain(), s.Record.Value)
	if r.status.Status == Core.Success {
		r.status.MG.AddInfo(resultMsg)
	} else {
		r.status.MG.AddError(resultMsg)
	}

	if err != nil {
		return err
	} else {
		return nil
	}
}

// MakeRequest  1.GetRecordId  2.DDNS
func (r *Request) MakeRequest() error {
	done := make(chan struct{})
	status := newStatus()
	var err error
	_ = Core.MainGoroutinePool.Submit(func() {
		*status, err = r.GetRecordId()
		done <- empty{}
	})

	s := &resOfddns{}

	content := ""
	select {
	case <-done:
		if err != nil || status.Status != Core.Success {
			r.status.Name = serviceName
			r.status.Status = Core.Failed
			for _, i := range status.MG.GetInfo() {
				r.status.MG.AddInfo(i.String())
			}

			for _, i := range status.MG.GetWarn() {
				r.status.MG.AddWarn(i.String())
			}

			for _, i := range status.MG.GetError() {
				r.status.MG.AddError(i.String())
			}
			r.status.MG.AddError(err.Error())
			return err
		}
		// content = Util.Convert2XWWWFormUrlencoded(&r.parameters)
		content = r.encodeURL()

	case <-time.After(time.Second * 20):
		r.status.Status = Core.Timeout
		r.status.MG.AddError("GetRecordId timeout")
		return errors.New("GetRecordId timeout")
	}

	log.Debugf("content:%s", content)
	client := Core.MainClientPool.Get().(*resty.Client)
	defer Core.MainClientPool.Put(client)
	response, err := client.R().SetResult(s).SetHeader("Content-Type", "application/x-www-form-urlencoded").SetBody([]byte(content)).Post(DDNSURL)
	log.Tracef("response: %v", response)
	log.Debugf("result:%+v", s)
	_ = json.Unmarshal(response.Body(), s)
	log.Debugf("after marshall:%+v", s)
	r.status = *code2status(s.Status.Code)
	resultMsg := fmt.Sprintf("%s at %s %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain(), s.Record.Value)
	if r.status.Status == Core.Success {
		r.status.MG.AddInfo(resultMsg)
	} else {
		r.status.MG.AddError(resultMsg)
	}
	if err != nil {
		return err
	} else {
		return nil
	}

}

// GetRecordId make request to Dnspod to get RecordId and set ExternalParameter.RecordId
func (r *Request) GetRecordId() (Core.Status, error) {
	if r.status.MG == nil {
		r.status.MG = Core.NewDefaultMsgGroup()
	}

	s := &resOfRecordId{}

	content := r.encodeURLWithoutID()

	log.Debugf("content:%s", content)

	// make request to "https://dnsapi.cn/Record.List" to get record id
	client := Core.MainClientPool.Get().(*resty.Client)
	defer Core.MainClientPool.Put(client)
	_, err := client.R().SetResult(s).SetHeader("Content-Type", "application/x-www-form-urlencoded").SetBody(content).Post(RecordListUrl)

	log.Debugf("after marshall:%s", s)
	status := *code2status(s.Status.Code)
	if err != nil {
		status.MG.AddError(fmt.Sprintf("%s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()))
		return status, err
	}

	if s.Status.Code != "1" {
		if s.Status.Code == "" {
			return status, errors.New("status code is empty")
		} else {
			status.MG.AddError(fmt.Sprintf("%s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()))
			return status, fmt.Errorf("status code:%s", s.Status.Code)
		}
	}

	if len(s.Records) == 0 {
		status.MG.AddError(fmt.Sprintf("%s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()))
		return status, fmt.Errorf("no record found")
	}

	id, err := strconv.Atoi(s.Records[0].Id)

	if err != nil {
		status.MG.AddError(fmt.Sprintf("%s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()))
		return status, err
	}

	status.MG.AddInfo(fmt.Sprintf("%s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()))
	r.parameters.RecordId = uint32(id)
	return status, nil
}

func (r *Request) GetRecordIdByProxy() (Core.Status, error) {
	if r.status.MG == nil {
		r.status.MG = Core.NewDefaultMsgGroup()
	}
	s := &resOfRecordId{}

	content := r.encodeURLWithoutID()
	log.Debugf("content:%s", content)

	client := Core.MainClientPool.Get().(*resty.Client)
	defer Core.MainClientPool.Put(client)
	res := client.R().SetHeader("Content-Type", "application/x-www-form-urlencoded")
	// make request to "https://dnsapi.cn/Record.List" to get record id
	iter := Net.GlobalProxies.GetProxyIter()
	for iter.NotLast() {
		proxy := iter.Next()
		_, err := res.SetBody(content).SetResult(s).Post(RecordListUrl)
		log.Debugf("after marshall:%s", s)
		if err == nil {
			break
		}

		errMsg := fmt.Sprintf("error get record id by proxy %s, error:%s", proxy, err.Error())
		r.status.MG.AddError(errMsg)
		log.ErrorRaw(errMsg)
		continue
	}
	status := code2status(s.Status.Code) // " %s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()

	if s.Status.Code != "1" {
		if s.Status.Code == "" {
			return *status, errors.New("status code is empty")
		} else {
			status.MG.AddError(fmt.Sprintf("%s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()))
			return *status, fmt.Errorf("status code:%s", s.Status.Code)
		}
	}

	if len(s.Records) == 0 {
		status.MG.AddError(fmt.Sprintf("%s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()))
		return *status, fmt.Errorf("no record found")
	}

	id, err := strconv.Atoi(s.Records[0].Id)

	if err != nil {
		status.MG.AddError(fmt.Sprintf("%s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()))
		return *status, err
	}
	status.MG.AddInfo(fmt.Sprintf("%s at %s %s", s.Status.Message, s.Status.CreatedAt, r.parameters.getTotalDomain()))
	r.parameters.RecordId = uint32(id)
	return *status, nil
}

type resOfRecordId struct {
	Status struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		CreatedAt string `json:"created_at"`
	} `json:"status"`

	Records []struct {
		Id            string `json:"id"`
		Ttl           string `json:"ttl"`
		Value         string `json:"value"`
		Enabled       string `json:"enabled"`
		Status        string `json:"status"`
		UpdatedOn     string `json:"updated_on"`
		RecordTypeV1  string `json:"record_type_v1"`
		Name          string `json:"name"`
		Line          string `json:"line"`
		LineId        string `json:"line_id"`
		Type          string `json:"type"`
		Weight        any    `json:"weight"`
		MonitorStatus string `json:"monitor_status"`
		Remark        string `json:"remark"`
		UseAqb        string `json:"use_aqb"`
		Mx            string `json:"mx"`
	} `json:"records"`
}

type resOfddns struct {
	Status struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		CreatedAt string `json:"created_at"`
	} `json:"status"`
	Record struct {
		Id    int    `json:"id"`
		Name  string `json:"name"`
		Value string `json:"value"`
	} `json:"record"`
}
