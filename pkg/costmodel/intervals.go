package costmodel

import (
	"sort"
	"time"

	"github.com/opencost/opencost/pkg/kubecost"
)

// IntervalPoint describes a start or end of a window of time
// Currently, this used in PVC-pod relations to detect/calculate
// coefficients for PV cost when a PVC is shared between pods.
type IntervalPoint struct {
	Time      time.Time
	PointType string
	Key       podKey
}

// IntervalPoints describes a slice of IntervalPoint structs
type IntervalPoints []IntervalPoint

// Requisite functions for implementing sort.Sort for
// IntervalPointList
func (ips IntervalPoints) Len() int {
	return len(ips)
}

func (ips IntervalPoints) Less(i, j int) bool {
	if ips[i].Time.Equal(ips[j].Time) {
		return ips[i].PointType == "start" && ips[j].PointType == "end"
	}
	return ips[i].Time.Before(ips[j].Time)
}

func (ips IntervalPoints) Swap(i, j int) {
	ips[i], ips[j] = ips[j], ips[i]
}

// NewIntervalPoint creates and returns a new IntervalPoint instance with given parameters.
func NewIntervalPoint(time time.Time, pointType string, key podKey) IntervalPoint {
	return IntervalPoint{
		Time:      time,
		PointType: pointType,
		Key:       key,
	}
}

// CoefficientComponent is a representative struct holding two fields which describe an interval
// as part of a single number cost coefficient calculation:
// 1. Proportion: The division of cost based on how many pods were running between those points
// 2. Time: The ratio of the time between those points to the total time that pod was running
type CoefficientComponent struct {
	Proportion float64
	Time       float64
}

// getIntervalPointFromWindows takes a map of podKeys to windows
// and returns a sorted list of IntervalPoints representing the
// starts and ends of all those windows.
func getIntervalPointsFromWindows(windows map[podKey]kubecost.Window) IntervalPoints {

	var intervals IntervalPoints

	for podKey, podWindow := range windows {

		start := NewIntervalPoint(*podWindow.Start(), "start", podKey)
		end := NewIntervalPoint(*podWindow.End(), "end", podKey)

		intervals = append(intervals, []IntervalPoint{start, end}...)

	}

	sort.Sort(intervals)

	return intervals

}

// getPVCCostCoefficients gets a coefficient which represents the scale
// factor that each PVC in a pvcIntervalMap and corresponding slice of
// IntervalPoints intervals uses to calculate a cost for that PVC's PV.
func getPVCCostCoefficients(intervals IntervalPoints, thisPVC *pvc, resolution time.Duration) map[podKey][]CoefficientComponent {
	// pvcCostCoefficientMap has a format such that the individual coefficient
	// components are preserved for testing purposes.
	pvcCostCoefficientMap := make(map[podKey][]CoefficientComponent)

	// Reset the start time by the offset as well. so that offset is not used in coefficient calculation!
	startTime := thisPVC.Start.Add(resolution)
	pvcWindow := kubecost.NewWindow(&startTime, &thisPVC.End)
	unmountedKey := getUnmountedPodKey(thisPVC.Cluster)

	var void struct{}
	activeKeys := map[podKey]struct{}{}

	currentTime := startTime
	// For each interval i.e. for any time a pod-PVC relation ends or starts...
	for _, point := range intervals {
		// If the current point happens at a later time than the previous point
		if !point.Time.Equal(currentTime) {
			for key := range activeKeys {
				pvcCostCoefficientMap[key] = append(
					pvcCostCoefficientMap[key],
					CoefficientComponent{
						Time:       point.Time.Sub(currentTime).Minutes() / pvcWindow.Duration().Minutes(),
						Proportion: 1.0 / float64(len(activeKeys)),
					},
				)

			}
			// If there are no active keys attribute all cost to the unmounted pv
			if len(activeKeys) == 0 {
				pvcCostCoefficientMap[unmountedKey] = append(
					pvcCostCoefficientMap[unmountedKey],
					CoefficientComponent{
						Time:       point.Time.Sub(currentTime).Minutes() / pvcWindow.Duration().Minutes(),
						Proportion: 1.0,
					},
				)
			}
		}

		// If the point was a start, increment and track
		if point.PointType == "start" {
			activeKeys[point.Key] = void
		}

		// If the point was an end, decrement and stop tracking
		if point.PointType == "end" {
			delete(activeKeys, point.Key)
		}

		currentTime = point.Time
	}

	// If all pod intervals end before the end of the PVC attribute the remaining cost to unmounted
	if currentTime.Before(thisPVC.End) {
		pvcCostCoefficientMap[unmountedKey] = append(
			pvcCostCoefficientMap[unmountedKey],
			CoefficientComponent{
				Time:       thisPVC.End.Sub(currentTime).Minutes() / pvcWindow.Duration().Minutes(),
				Proportion: 1.0,
			},
		)
	}
	return pvcCostCoefficientMap
}

// getCoefficientFromComponents takes the components of a PVC-pod PV cost coefficient
// determined by getPVCCostCoefficient and gets the resulting single
// floating point coefficient.
func getCoefficientFromComponents(coefficientComponents []CoefficientComponent) float64 {

	coefficient := 0.0

	for i := range coefficientComponents {

		proportion := coefficientComponents[i].Proportion
		time := coefficientComponents[i].Time

		coefficient += proportion * time

	}

	return coefficient
}
