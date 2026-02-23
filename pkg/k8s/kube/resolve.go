package kube

import (
	"context"
	"fmt"
	"sort"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
)

// GetFirstPodForDeployment returns the name of the best candidate pod owned by
// the given deployment. It fetches the deployment's label selector, lists
// matching pods, filters out ineligible ones (deleting, terminal phase,
// unassigned), and ranks the rest using the same criteria as kubectl's
// ByLogging sort — preferring running, ready, long-lived, low-restart pods.
func GetFirstPodForDeployment(ctx context.Context, cs kubernetes.Interface, namespace, deploymentName string) (string, error) {
	deploy, err := cs.AppsV1().Deployments(namespace).Get(ctx, deploymentName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get deployment %s/%s: %w", namespace, deploymentName, err)
	}

	sel := labels.Set(deploy.Spec.Selector.MatchLabels).String()
	pods, err := cs.CoreV1().Pods(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: sel,
	})
	if err != nil {
		return "", fmt.Errorf("list pods for deployment %s/%s: %w", namespace, deploymentName, err)
	}

	best := pickBestPod(pods.Items)
	if best == nil {
		return "", fmt.Errorf("no eligible pods for deployment %s/%s (%d total)", namespace, deploymentName, len(pods.Items))
	}
	return best.Name, nil
}

// pickBestPod filters out ineligible pods and returns the best candidate using
// kubectl's ByLogging ranking. Returns nil if no eligible pods remain.
func pickBestPod(pods []corev1.Pod) *corev1.Pod {
	var eligible []*corev1.Pod
	for i := range pods {
		p := &pods[i]
		// Skip pods marked for deletion.
		if p.DeletionTimestamp != nil {
			continue
		}
		// Skip terminal phases.
		if p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed {
			continue
		}
		// Skip unassigned pods.
		if p.Spec.NodeName == "" {
			continue
		}
		eligible = append(eligible, p)
	}
	if len(eligible) == 0 {
		return nil
	}

	// Sort best-first using kubectl's ByLogging criteria.
	sort.Sort(byHealth(eligible))
	return eligible[0]
}

// byHealth sorts pods best-first, mirroring kubectl's ByLogging algorithm.
// Criteria in priority order:
//  1. Running > Unknown > Pending
//  2. Ready > not ready
//  3. Ready for longer > shorter
//  4. Fewer container restarts > more
//  5. Older (earlier creation) > newer
type byHealth []*corev1.Pod

func (s byHealth) Len() int      { return len(s) }
func (s byHealth) Swap(i, j int) { s[i], s[j] = s[j], s[i] }

func (s byHealth) Less(i, j int) bool {
	pi, pj := s[i], s[j]

	// 1. Pod phase: Running < Unknown < Pending (lower = better).
	phaseRank := map[corev1.PodPhase]int{
		corev1.PodRunning: 0,
		corev1.PodUnknown: 1,
		corev1.PodPending: 2,
	}
	ri, rj := phaseRank[pi.Status.Phase], phaseRank[pj.Status.Phase]
	if ri != rj {
		return ri < rj
	}

	// 2. Ready > not ready.
	readyI, readyJ := isPodReady(pi), isPodReady(pj)
	if readyI != readyJ {
		return readyI
	}

	// 3. Ready for longer is better.
	if readyI && readyJ {
		ti, tj := podReadyTime(pi), podReadyTime(pj)
		if !ti.Equal(tj) {
			return afterOrZero(tj, ti)
		}
	}

	// 4. Fewer total container restarts is better.
	ri, rj = maxContainerRestarts(pi), maxContainerRestarts(pj)
	if ri != rj {
		return ri < rj
	}

	// 5. Older pod is better.
	if !pi.CreationTimestamp.Equal(&pj.CreationTimestamp) {
		return afterOrZero(&pj.CreationTimestamp, &pi.CreationTimestamp)
	}

	return false
}

// isPodReady returns true when the pod has a PodReady condition set to True.
func isPodReady(pod *corev1.Pod) bool {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// podReadyTime returns the LastTransitionTime of the PodReady condition, or
// a zero Time if not found.
func podReadyTime(pod *corev1.Pod) *metav1.Time {
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return &c.LastTransitionTime
		}
	}
	return &metav1.Time{}
}

// maxContainerRestarts returns the highest restart count across all containers.
func maxContainerRestarts(pod *corev1.Pod) int {
	var m int32
	for _, cs := range pod.Status.ContainerStatuses {
		if cs.RestartCount > m {
			m = cs.RestartCount
		}
	}
	for _, cs := range pod.Status.InitContainerStatuses {
		if cs.RestartCount > m {
			m = cs.RestartCount
		}
	}
	return int(m)
}

// afterOrZero returns true if t1 is after t2, treating a zero time as "after
// everything" (i.e. unknown/empty sorts last).
func afterOrZero(t1, t2 *metav1.Time) bool {
	if t1.IsZero() || t2.IsZero() {
		return t2.IsZero()
	}
	return t1.After(t2.Time)
}
