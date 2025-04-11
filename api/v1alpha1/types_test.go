package v1alpha1

import (
	"testing"

	"github.com/onsi/gomega"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestLogRemediationValidation(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Test creating a valid LogRemediation
	validLogRemediation := &LogRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-remediation",
			Namespace: "default",
		},
		Spec: LogRemediationSpec{
			Sources: []LogSource{
				{
					Type: "deployment",
					Selector: map[string]string{
						"app": "test-app",
					},
				},
			},
			ElasticsearchConfig: ElasticsearchConfig{
				Host:  "elasticsearch.default.svc",
				Port:  9200,
				Index: "kubernetes-logs",
			},
			RemediationRules: []RemediationRule{
				{
					ErrorPattern:   "ERROR.*",
					Action:         "restart",
					CooldownPeriod: 60,
				},
			},
		},
	}

	// Verify required fields are present
	g.Expect(validLogRemediation.Spec.Sources).NotTo(gomega.BeEmpty())
	g.Expect(validLogRemediation.Spec.ElasticsearchConfig.Host).NotTo(gomega.BeEmpty())
	g.Expect(validLogRemediation.Spec.ElasticsearchConfig.Index).NotTo(gomega.BeEmpty())

	// Test with FluentbitConfig
	validLogRemediation.Spec.FluentbitConfig = &FluentbitConfig{
		BufferSize:    "10MB",
		FlushInterval: 5,
		Parser:        "custom-parser",
	}

	g.Expect(validLogRemediation.Spec.FluentbitConfig.BufferSize).To(gomega.Equal("10MB"))
	g.Expect(validLogRemediation.Spec.FluentbitConfig.FlushInterval).To(gomega.Equal(int32(5)))
}

func TestRemediationHistoryEntry(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	// Create a history entry
	entry := RemediationHistoryEntry{
		Timestamp: metav1.Now(),
		PodName:   "test-pod",
		Pattern:   "ERROR.*",
		Action:    "restart",
	}

	// Validate fields
	g.Expect(entry.PodName).To(gomega.Equal("test-pod"))
	g.Expect(entry.Pattern).To(gomega.Equal("ERROR.*"))
	g.Expect(entry.Action).To(gomega.Equal("restart"))
}

func TestLogRemediationDeepCopy(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	original := &LogRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-remediation",
			Namespace: "default",
		},
		Spec: LogRemediationSpec{
			Sources: []LogSource{
				{
					Type: "deployment",
					Selector: map[string]string{
						"app": "test-app",
					},
				},
			},
			ElasticsearchConfig: ElasticsearchConfig{
				Host:  "elasticsearch.default.svc",
				Port:  9200,
				Index: "kubernetes-logs",
			},
		},
		Status: LogRemediationStatus{
			ObservedGeneration: 1,
			FluentbitPods:      []string{"pod1", "pod2"},
			RemediationHistory: []RemediationHistoryEntry{
				{
					Timestamp: metav1.Now(),
					PodName:   "test-pod",
					Pattern:   "ERROR.*",
					Action:    "restart",
				},
			},
		},
	}

	// Test DeepCopy
	copy := original.DeepCopy()
	g.Expect(copy.Name).To(gomega.Equal(original.Name))
	g.Expect(copy.Namespace).To(gomega.Equal(original.Namespace))
	g.Expect(copy.Spec.Sources[0].Type).To(gomega.Equal(original.Spec.Sources[0].Type))
	g.Expect(copy.Status.FluentbitPods).To(gomega.Equal(original.Status.FluentbitPods))
	g.Expect(copy.Status.RemediationHistory[0].PodName).To(gomega.Equal(original.Status.RemediationHistory[0].PodName))

	// Test DeepCopyObject
	copyObj := original.DeepCopyObject()
	copyLogRemediation, ok := copyObj.(*LogRemediation)
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(copyLogRemediation.Name).To(gomega.Equal(original.Name))

	// Test DeepCopyInto
	target := &LogRemediation{}
	original.DeepCopyInto(target)
	g.Expect(target.Name).To(gomega.Equal(original.Name))
	g.Expect(target.Namespace).To(gomega.Equal(original.Namespace))
	g.Expect(target.Spec.Sources[0].Type).To(gomega.Equal(original.Spec.Sources[0].Type))
}

func TestLogRemediationListDeepCopy(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	item := LogRemediation{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-remediation",
			Namespace: "default",
		},
		Spec: LogRemediationSpec{
			Sources: []LogSource{
				{
					Type: "deployment",
					Selector: map[string]string{
						"app": "test-app",
					},
				},
			},
		},
	}

	original := &LogRemediationList{
		Items: []LogRemediation{item},
	}

	// Test DeepCopy
	copy := original.DeepCopy()
	g.Expect(copy.Items).To(gomega.HaveLen(1))
	g.Expect(copy.Items[0].Name).To(gomega.Equal(original.Items[0].Name))

	// Test DeepCopyObject
	copyObj := original.DeepCopyObject()
	copyList, ok := copyObj.(*LogRemediationList)
	g.Expect(ok).To(gomega.BeTrue())
	g.Expect(copyList.Items).To(gomega.HaveLen(1))
	g.Expect(copyList.Items[0].Name).To(gomega.Equal(original.Items[0].Name))
}

func TestLogSourceDeepCopy(t *testing.T) {
	g := gomega.NewGomegaWithT(t)

	original := LogSource{
		Type: "deployment",
		Selector: map[string]string{
			"app": "test-app",
		},
		Container: "container-name",
		Path:      "/var/log/path",
	}

	// Test DeepCopy
	copy := original.DeepCopy()
	g.Expect(copy.Type).To(gomega.Equal(original.Type))
	g.Expect(copy.Selector).To(gomega.Equal(original.Selector))
	g.Expect(copy.Container).To(gomega.Equal(original.Container))
	g.Expect(copy.Path).To(gomega.Equal(original.Path))

	// Modify the copy and verify it doesn't affect the original
	copy.Selector["new-key"] = "new-value"
	g.Expect(original.Selector).NotTo(gomega.HaveKey("new-key"))

	// Test DeepCopyInto
	target := LogSource{}
	original.DeepCopyInto(&target)
	g.Expect(target.Type).To(gomega.Equal(original.Type))
	g.Expect(target.Selector).To(gomega.Equal(original.Selector))
}
