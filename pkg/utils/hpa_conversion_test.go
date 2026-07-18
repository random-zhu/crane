package utils

import (
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
)

func TestMetricSpecsCRDBoundaryRoundTrip(t *testing.T) {
	utilization := int32(75)
	source := []autoscalingv2.MetricSpec{{
		Type: autoscalingv2.ResourceMetricSourceType,
		Resource: &autoscalingv2.ResourceMetricSource{
			Name:   corev1.ResourceCPU,
			Target: autoscalingv2.MetricTarget{Type: autoscalingv2.UtilizationMetricType, AverageUtilization: &utilization},
		},
	}}
	beta, err := MetricSpecsToV2Beta2(source)
	if err != nil {
		t.Fatal(err)
	}
	result, err := MetricSpecsToV2(beta)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 1 || result[0].Resource == nil || *result[0].Resource.Target.AverageUtilization != utilization {
		t.Fatalf("unexpected conversion result: %+v", result)
	}
}
