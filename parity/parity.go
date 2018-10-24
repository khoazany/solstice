package parity

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type jsonrpcResponse struct {
	Jsonrpc string
	Result  execTrace
	Id      int
}

type execTrace struct {
	Trace     []interface{}
	StateDiff interface{}
	Output    string
	VmTrace   VmTrace
}

type VmTrace struct {
	Ops  []traceOperation
	Code string
}

type traceOperation struct {
	Cost int
	Pc   int
	Sub  interface{}
	Ex   execTraceOpEx
}

type execTraceOpEx struct {
	Push  []string
	Mem   interface{}
	Used  int
	Store interface{}
}

func GetExecTrace(txnHash string) (VmTrace, error) {
	resp, err := http.Post(
		"http://127.0.0.1:8545",
		"application/json",
		strings.NewReader(
			fmt.Sprintf(
				`{
					"jsonrpc": "2.0",
					"method": "trace_replayTransaction",
					"params": [
						%q,
						["vmTrace"]
					],
					"id": 1
				}`,
				txnHash,
			),
		),
	)
	if err != nil {
		return VmTrace{}, err
	}
	defer resp.Body.Close()
	var execTraceResponse jsonrpcResponse
	err = json.NewDecoder(resp.Body).Decode(&execTraceResponse)

	return execTraceResponse.Result.VmTrace, err
}
