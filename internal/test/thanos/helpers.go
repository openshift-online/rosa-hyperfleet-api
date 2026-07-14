package thanos

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/onsi/ginkgo/v2"
	awstest "github.com/openshift/rosa-hyperfleet-api/internal/test/aws"
)

type QueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

type RulesResponse struct {
	Status string `json:"status"`
	Data   struct {
		Groups []RuleGroup `json:"groups"`
	} `json:"data"`
}

type RuleGroup struct {
	Name  string `json:"name"`
	Rules []Rule `json:"rules"`
}

type Rule struct {
	Name   string            `json:"name"`
	Type   string            `json:"type"`
	Query  string            `json:"query"`
	Labels map[string]string `json:"labels"`
	State  string            `json:"state"`
}

func Query(client *awstest.APIClient, promql string) QueryResponse {
	path := fmt.Sprintf("/api/v1/query?query=%s", url.QueryEscape(promql))
	resp, err := client.Get(path, "")
	if err != nil {
		ginkgo.GinkgoWriter.Printf("Thanos query error: %v\n", err)
		return QueryResponse{}
	}
	if resp.StatusCode != http.StatusOK {
		ginkgo.GinkgoWriter.Printf("Thanos query returned %d: %s\n", resp.StatusCode, string(resp.Body))
		return QueryResponse{}
	}

	var result QueryResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		ginkgo.GinkgoWriter.Printf("Failed to parse Thanos response: %v\n", err)
		return QueryResponse{}
	}

	ginkgo.GinkgoWriter.Printf("Thanos query: %s → %d results\n", promql, len(result.Data.Result))
	return result
}

func HasRule(client *awstest.APIClient, ruleType, ruleName string) bool {
	rules := QueryRules(client, ruleType)
	for _, group := range rules.Data.Groups {
		for _, r := range group.Rules {
			if r.Name == ruleName {
				ginkgo.GinkgoWriter.Printf("Found rule %s (type=%s, state=%s)\n", r.Name, r.Type, r.State)
				return true
			}
		}
	}
	return false
}

func QueryRules(client *awstest.APIClient, ruleType string) RulesResponse {
	path := fmt.Sprintf("/api/v1/rules?type=%s", url.QueryEscape(ruleType))
	resp, err := client.Get(path, "")
	if err != nil {
		ginkgo.GinkgoWriter.Printf("Thanos rules query error: %v\n", err)
		return RulesResponse{}
	}
	if resp.StatusCode != http.StatusOK {
		ginkgo.GinkgoWriter.Printf("Thanos rules query returned %d: %s\n", resp.StatusCode, string(resp.Body))
		return RulesResponse{}
	}

	var result RulesResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		ginkgo.GinkgoWriter.Printf("Failed to parse Thanos rules response: %v\n", err)
		return RulesResponse{}
	}

	totalRules := 0
	for _, g := range result.Data.Groups {
		totalRules += len(g.Rules)
	}
	ginkgo.GinkgoWriter.Printf("Thanos rules query (type=%s) → %d groups, %d rules\n",
		ruleType, len(result.Data.Groups), totalRules)
	return result
}
