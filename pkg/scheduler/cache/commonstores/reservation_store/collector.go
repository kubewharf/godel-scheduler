/*
Copyright 2023 The Godel Scheduler Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package reservationstore

import (
	"strconv"

	"github.com/kubewharf/godel-scheduler/pkg/framework/api"
	schedmetrics "github.com/kubewharf/godel-scheduler/pkg/scheduler/metrics"
	podutil "github.com/kubewharf/godel-scheduler/pkg/util/pod"

	v1 "k8s.io/api/core/v1"
)

const (
	expiredStatus   = "expired"
	availableStatus = "available"
	matchedStatus   = "matched"
)

// {subCluster, qos, priority, status}
type tagTuple [4]string

type metricTag struct {
	qos        string
	subCluster string
	priority   string
	status     string
}

func (m metricTag) getTuple() tagTuple {
	return tagTuple{m.subCluster, m.qos, m.priority, m.status}
}

func newMetricTag(subCluster string, qos podutil.PodResourceType, priority int32, status string) metricTag {
	return metricTag{
		subCluster: subCluster,
		qos:        string(qos),
		priority:   strconv.Itoa(int(priority)),
		status:     status,
	}
}

type collector struct {
	existingReservationInfos map[string]metricTag
	expiredReservationInfos  map[metricTag]int
}

func newCollector() *collector {
	return &collector{
		existingReservationInfos: make(map[string]metricTag),
		expiredReservationInfos:  make(map[metricTag]int),
	}
}

func (c *collector) add(fakePod *v1.Pod, status string) {
	p := api.ExtractPodProperty(fakePod)
	tag := newMetricTag(p.SubCluster, p.Qos, p.Priority, status)
	key := podutil.GetPodKey(fakePod)
	c.existingReservationInfos[key] = tag
}

func (c *collector) delete(fakePod *v1.Pod) {
	key := podutil.GetPodKey(fakePod)
	delete(c.existingReservationInfos, key)
}

func (c *collector) expire(fakePod *v1.Pod) {
	c.delete(fakePod)

	p := api.ExtractPodProperty(fakePod)
	tag := newMetricTag(p.SubCluster, p.Qos, p.Priority, expiredStatus)
	if _, ok := c.expiredReservationInfos[tag]; ok {
		c.expiredReservationInfos[tag]++
	} else {
		c.expiredReservationInfos[tag] = 1
	}
}

func (c *collector) emit() {
	reservationInfoCount.Reset()

	infos := make(map[tagTuple]int, 0)
	for _, info := range c.existingReservationInfos {
		tags := info.getTuple()
		infos[tags]++
	}

	for tag, v := range infos {
		reservationInfoCount.WithLabelValues(tag[0], tag[1], tag[2], tag[3], schedmetrics.SchedulerName).Set(float64(v))
	}

	for tag, v := range c.expiredReservationInfos {
		expiredReservationInfoCount.WithLabelValues(tag.subCluster, tag.qos, tag.priority, schedmetrics.SchedulerName).Add(float64(v))
	}
}
