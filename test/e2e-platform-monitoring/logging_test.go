package e2e_monitoring_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	awstest "github.com/openshift/rosa-hyperfleet-api/internal/test/aws"
)

type lokiQueryResponse struct {
	Status string `json:"status"`
	Data   struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Stream map[string]string `json:"stream"`
			Values [][]string        `json:"values"`
		} `json:"result"`
	} `json:"data"`
}

var _ = Describe("Logging", Ordered, func() {

	It("should have logs from regional-cluster in Loki", func() {
		query := `{cluster_type="regional-cluster"}`
		Eventually(func() bool {
			resp := queryLoki(rhobsClient, query)
			return resp.Status == "success" && len(resp.Data.Result) > 0
		}, "1m", "15s").Should(BeTrue(),
			"Expected logs with cluster_type=regional-cluster in Loki "+
				"(Vector DaemonSet → Loki Distributor)")
	})

	It("should have logs from management-cluster in Loki", func() {
		query := `{cluster_type="management-cluster"}`
		Eventually(func() bool {
			resp := queryLoki(rhobsClient, query)
			return resp.Status == "success" && len(resp.Data.Result) > 0
		}, "1m", "15s").Should(BeTrue(),
			"Expected logs with cluster_type=management-cluster in Loki "+
				"(Vector DaemonSet → sigv4-proxy → RHOBS API GW → Loki Distributor)")
	})

})

func queryLoki(client *awstest.APIClient, logql string) lokiQueryResponse {
	path := fmt.Sprintf("/loki/api/v1/query_range?query=%s&limit=10&since=1h",
		url.QueryEscape(logql))
	resp, err := client.Get(path, "")
	if err != nil {
		GinkgoWriter.Printf("Loki query error: %v\n", err)
		return lokiQueryResponse{}
	}
	if resp.StatusCode != http.StatusOK {
		GinkgoWriter.Printf("Loki query returned %d: %s\n", resp.StatusCode, string(resp.Body))
		return lokiQueryResponse{}
	}

	var result lokiQueryResponse
	if err := json.Unmarshal(resp.Body, &result); err != nil {
		GinkgoWriter.Printf("Failed to parse Loki response: %v\n", err)
		return lokiQueryResponse{}
	}

	totalEntries := 0
	for _, stream := range result.Data.Result {
		totalEntries += len(stream.Values)
	}
	GinkgoWriter.Printf("Loki query: %s → %d streams, %d entries\n", logql, len(result.Data.Result), totalEntries)
	return result
}
