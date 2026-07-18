package ehpa

import (
	"context"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/gocrane/crane/pkg/metrics"
	"github.com/gocrane/crane/pkg/utils"
)

type hpaEventHandler struct {
	enqueueHandler handler.TypedEnqueueRequestForObject[*autoscalingv2.HorizontalPodAutoscaler]
}

func (h *hpaEventHandler) Create(ctx context.Context, evt event.TypedCreateEvent[*autoscalingv2.HorizontalPodAutoscaler], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	pod := evt.Object
	if pod.DeletionTimestamp != nil {
		h.Delete(ctx, event.TypedDeleteEvent[*autoscalingv2.HorizontalPodAutoscaler]{Object: evt.Object}, q)
		return
	}

	h.enqueueHandler.Create(ctx, evt, q)
}

func (h *hpaEventHandler) Delete(ctx context.Context, evt event.TypedDeleteEvent[*autoscalingv2.HorizontalPodAutoscaler], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	h.enqueueHandler.Delete(ctx, evt, q)
}

func (h *hpaEventHandler) Update(ctx context.Context, evt event.TypedUpdateEvent[*autoscalingv2.HorizontalPodAutoscaler], q workqueue.TypedRateLimitingInterface[reconcile.Request]) {
	newHpa := evt.ObjectNew
	oldHpa := evt.ObjectOld
	klog.V(6).Infof("hpa %s OnUpdate", klog.KObj(newHpa))
	if oldHpa.Status.DesiredReplicas != newHpa.Status.DesiredReplicas {
		for _, cond := range newHpa.Status.Conditions {
			if cond.Reason == "SucceededRescale" || cond.Reason == "SucceededOverloadRescale" {
				scaleType := "hpa"
				if utils.IsHPAControlledByEHPA(newHpa) {
					scaleType = "ehpa"
				}

				labels := map[string]string{
					"namespace": newHpa.Namespace,
					"name":      newHpa.Name,
					"type":      scaleType,
				}
				metrics.HPAScaleCount.With(labels).Inc()

				break
			}
		}
	}
	h.enqueueHandler.Update(ctx, evt, q)
}

func (h *hpaEventHandler) Generic(context.Context, event.TypedGenericEvent[*autoscalingv2.HorizontalPodAutoscaler], workqueue.TypedRateLimitingInterface[reconcile.Request]) {
}
