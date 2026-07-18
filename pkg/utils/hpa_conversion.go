package utils

import (
	"encoding/json"
	"fmt"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	autoscalingv2beta2 "k8s.io/api/autoscaling/v2beta2"
)

// The external gocrane/api CRDs still expose v2beta2-shaped embedded fields.
// Kubernetes no longer serves autoscaling/v2beta2 HPAs, so all runtime HPA
// objects use autoscaling/v2 and conversion is isolated at this CRD boundary.
func MetricSpecsToV2(source []autoscalingv2beta2.MetricSpec) ([]autoscalingv2.MetricSpec, error) {
	if source == nil {
		return nil, nil
	}
	result := make([]autoscalingv2.MetricSpec, 0, len(source))
	if err := convertAutoscalingJSON(source, &result); err != nil {
		return nil, fmt.Errorf("convert metric specs to autoscaling/v2: %w", err)
	}
	return result, nil
}

func MetricSpecsToV2Beta2(source []autoscalingv2.MetricSpec) ([]autoscalingv2beta2.MetricSpec, error) {
	if source == nil {
		return nil, nil
	}
	result := make([]autoscalingv2beta2.MetricSpec, 0, len(source))
	if err := convertAutoscalingJSON(source, &result); err != nil {
		return nil, fmt.Errorf("convert metric specs to autoscaling/v2beta2 CRD field: %w", err)
	}
	return result, nil
}

func HPABehaviorToV2(source *autoscalingv2beta2.HorizontalPodAutoscalerBehavior) (*autoscalingv2.HorizontalPodAutoscalerBehavior, error) {
	if source == nil {
		return nil, nil
	}
	result := &autoscalingv2.HorizontalPodAutoscalerBehavior{}
	if err := convertAutoscalingJSON(source, result); err != nil {
		return nil, fmt.Errorf("convert HPA behavior to autoscaling/v2: %w", err)
	}
	return result, nil
}

func CrossVersionObjectReferenceToV2(source autoscalingv2beta2.CrossVersionObjectReference) autoscalingv2.CrossVersionObjectReference {
	return autoscalingv2.CrossVersionObjectReference{
		Kind: source.Kind, Name: source.Name, APIVersion: source.APIVersion,
	}
}

func CrossVersionObjectReferenceToV2Beta2(source autoscalingv2.CrossVersionObjectReference) autoscalingv2beta2.CrossVersionObjectReference {
	return autoscalingv2beta2.CrossVersionObjectReference{
		Kind: source.Kind, Name: source.Name, APIVersion: source.APIVersion,
	}
}

func CrossVersionObjectReferencePointerToV2Beta2(source autoscalingv2.CrossVersionObjectReference) *autoscalingv2beta2.CrossVersionObjectReference {
	result := CrossVersionObjectReferenceToV2Beta2(source)
	return &result
}

func convertAutoscalingJSON(source, destination interface{}) error {
	data, err := json.Marshal(source)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, destination)
}
