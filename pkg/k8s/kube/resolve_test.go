package kube

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func pod(name string, opts ...func(*corev1.Pod)) corev1.Pod {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.PodSpec{NodeName: "node-1"},
		Status:     corev1.PodStatus{Phase: corev1.PodRunning},
	}
	for _, o := range opts {
		o(&p)
	}
	return p
}

func withPhase(phase corev1.PodPhase) func(*corev1.Pod) {
	return func(p *corev1.Pod) { p.Status.Phase = phase }
}

func withReady(t time.Time) func(*corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Status.Conditions = []corev1.PodCondition{{
			Type:               corev1.PodReady,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.NewTime(t),
		}}
	}
}

func withRestarts(count int32) func(*corev1.Pod) {
	return func(p *corev1.Pod) {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{
			RestartCount: count,
		}}
	}
}

func withCreation(t time.Time) func(*corev1.Pod) {
	return func(p *corev1.Pod) { p.CreationTimestamp = metav1.NewTime(t) }
}

func withDeleting() func(*corev1.Pod) {
	return func(p *corev1.Pod) {
		now := metav1.Now()
		p.DeletionTimestamp = &now
	}
}

func withUnassigned() func(*corev1.Pod) {
	return func(p *corev1.Pod) { p.Spec.NodeName = "" }
}

func TestPickBestPod(t *testing.T) {
	now := time.Now()
	earlier := now.Add(-10 * time.Minute)

	tests := []struct {
		name     string
		pods     []corev1.Pod
		wantName string
		wantNil  bool
	}{
		{
			name:    "no pods",
			pods:    nil,
			wantNil: true,
		},
		{
			name:    "all deleting",
			pods:    []corev1.Pod{pod("a", withDeleting()), pod("b", withDeleting())},
			wantNil: true,
		},
		{
			name:    "all terminal",
			pods:    []corev1.Pod{pod("a", withPhase(corev1.PodSucceeded)), pod("b", withPhase(corev1.PodFailed))},
			wantNil: true,
		},
		{
			name:    "all unassigned",
			pods:    []corev1.Pod{pod("a", withUnassigned()), pod("b", withUnassigned())},
			wantNil: true,
		},
		{
			name:     "single eligible",
			pods:     []corev1.Pod{pod("only")},
			wantName: "only",
		},
		{
			name:     "skip deleting pod",
			pods:     []corev1.Pod{pod("deleting", withDeleting()), pod("good")},
			wantName: "good",
		},
		{
			name: "running beats pending",
			pods: []corev1.Pod{
				pod("pending", withPhase(corev1.PodPending)),
				pod("running"),
			},
			wantName: "running",
		},
		{
			name: "ready beats not ready",
			pods: []corev1.Pod{
				pod("not-ready"),
				pod("ready", withReady(now)),
			},
			wantName: "ready",
		},
		{
			name: "longer ready beats shorter",
			pods: []corev1.Pod{
				pod("new-ready", withReady(now)),
				pod("old-ready", withReady(earlier)),
			},
			wantName: "old-ready",
		},
		{
			name: "fewer restarts beats more",
			pods: []corev1.Pod{
				pod("crashy", withReady(now), withRestarts(5)),
				pod("stable", withReady(now), withRestarts(0)),
			},
			wantName: "stable",
		},
		{
			name: "older beats newer as tiebreaker",
			pods: []corev1.Pod{
				pod("new", withCreation(now)),
				pod("old", withCreation(earlier)),
			},
			wantName: "old",
		},
		{
			name: "full ranking: deleting + pending + not-ready + crashy + good",
			pods: []corev1.Pod{
				pod("deleting", withDeleting(), withReady(earlier)),
				pod("pending", withPhase(corev1.PodPending)),
				pod("not-ready", withRestarts(0), withCreation(earlier)),
				pod("crashy", withReady(earlier), withRestarts(10)),
				pod("good", withReady(earlier), withRestarts(0)),
			},
			wantName: "good",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pickBestPod(tt.pods)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("pickBestPod() = %q, want nil", got.Name)
				}
				return
			}
			if got == nil {
				t.Fatal("pickBestPod() = nil, want non-nil")
			}
			if got.Name != tt.wantName {
				t.Errorf("pickBestPod() = %q, want %q", got.Name, tt.wantName)
			}
		})
	}
}
