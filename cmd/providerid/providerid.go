package providerid

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager/signals"

	"github.com/go-logr/logr"
	"github.com/spf13/cobra"
	"go.uber.org/zap/zapcore"
	"google.golang.org/api/compute/v1"
	"google.golang.org/api/option"
)

type Options struct {
	Kubeconfig string
	Project    string
	Region     string
	LogLevel   string
}

func NewCommand() *cobra.Command {
	opts := &Options{}

	cmd := &cobra.Command{
		Use:          "providerid-controller",
		Short:        "Runs the GCP providerID controller",
		SilenceUsage: true,
	}

	cmd.Flags().StringVar(&opts.Kubeconfig, "kubeconfig", "", "Path to kubeconfig for guest cluster")
	cmd.Flags().StringVar(&opts.Project, "project", "", "GCP project ID")
	cmd.Flags().StringVar(&opts.Region, "region", "", "GCP region")
	cmd.Flags().StringVar(&opts.LogLevel, "log-level", "info", "Log level (debug, info, warn, error)")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		return opts.Run(cmd.Context())
	}

	return cmd
}

func (o *Options) Run(ctx context.Context) error {
	// Setup logger
	level := zapcore.InfoLevel
	switch o.LogLevel {
	case "debug":
		level = zapcore.DebugLevel
	case "warn":
		level = zapcore.WarnLevel
	case "error":
		level = zapcore.ErrorLevel
	}

	logger := zap.New(zap.Level(level), zap.JSONEncoder(func(config *zapcore.EncoderConfig) {
		config.EncodeTime = zapcore.RFC3339TimeEncoder
	}))
	ctrl.SetLogger(logger)

	log := logger.WithName("providerid-controller")
	log.Info("Starting GCP providerID controller",
		"project", o.Project,
		"region", o.Region)

	// Load guest cluster kubeconfig
	config, err := clientcmd.BuildConfigFromFlags("", o.Kubeconfig)
	if err != nil {
		return fmt.Errorf("failed to load kubeconfig: %w", err)
	}

	// Create Kubernetes client
	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return fmt.Errorf("failed to create kubernetes client: %w", err)
	}

	// Create GCP Compute client
	gcpClient, err := compute.NewService(ctx, option.WithCredentialsFile(os.Getenv("GOOGLE_APPLICATION_CREDENTIALS")))
	if err != nil {
		return fmt.Errorf("failed to create GCP compute client: %w", err)
	}

	controller := &ProviderIDController{
		clientset: clientset,
		gcpClient: gcpClient,
		project:   o.Project,
		region:    o.Region,
		log:       log,
	}

	// Setup signal handler
	ctx = signals.SetupSignalHandler()

	// Run reconciliation loop
	return controller.Run(ctx)
}

type ProviderIDController struct {
	clientset *kubernetes.Clientset
	gcpClient *compute.Service
	project   string
	region    string
	log       logr.Logger
}

const (
	cloudTaintKey = "node.cloudprovider.kubernetes.io/uninitialized"
	zoneLabel     = "topology.kubernetes.io/zone"
)

// extractInstanceName extracts the GCP instance name from the node name.
// It handles multiple formats:
// 1. GCP internal DNS: <instance-name>.<zone>.c.<project-id>.internal
// 2. Short hostname: <instance-name>.<zone>.<domain>
// 3. Plain instance name: <instance-name>
//
// Returns the instance name part (first segment before any dots).
func extractInstanceName(nodeName string) string {
	// Split by '.' and take the first part
	parts := strings.Split(nodeName, ".")
	if len(parts) > 0 && parts[0] != "" {
		return parts[0]
	}
	// Fallback to full name if parsing fails or is empty
	return nodeName
}

// extractZoneFromNodeName attempts to extract the zone from the node's DNS name.
// It handles multiple formats:
// 1. GCP internal DNS: <instance-name>.<zone>.c.<project-id>.internal
// 2. Short hostname: <instance-name>.<zone>.<domain>
//
// Returns empty string if the format doesn't match expectations or zone cannot be determined.
func extractZoneFromNodeName(nodeName string) string {
	parts := strings.Split(nodeName, ".")
	// Need at least 2 parts: instance.zone or more
	if len(parts) >= 2 {
		// The zone is typically the second component
		zone := parts[1]
		// Basic validation: GCP zone should contain hyphens (e.g., us-central1-a)
		// This helps avoid treating random domain parts as zones
		if strings.Contains(zone, "-") && len(zone) > 3 {
			return zone
		}
	}
	return ""
}

// validateInstanceName checks if the instance name conforms to GCP naming requirements.
// GCP instance names must match: [a-z](?:[-a-z0-9]{0,61}[a-z0-9])?|[1-9][0-9]{0,19}
// - Must start with a lowercase letter or digit
// - Can contain lowercase letters, numbers, and hyphens
// - Must be 1-63 characters long
// - Cannot end with a hyphen
func validateInstanceName(name string) error {
	if name == "" {
		return fmt.Errorf("instance name cannot be empty")
	}

	if len(name) > 63 {
		return fmt.Errorf("instance name too long (%d chars, max 63): %s", len(name), name)
	}

	// Check for valid characters and format
	validNamePattern := regexp.MustCompile(`^[a-z]([a-z0-9-]{0,61}[a-z0-9])?$|^[1-9][0-9]{0,19}$`)
	if !validNamePattern.MatchString(name) {
		return fmt.Errorf("instance name '%s' does not match GCP naming requirements (must start with lowercase letter, contain only lowercase letters, numbers, and hyphens, and not end with hyphen)", name)
	}

	return nil
}

func (c *ProviderIDController) Run(ctx context.Context) error {
	c.log.Info("Starting reconciliation loop")

	// Run reconciliation every 30 seconds
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	// Run immediately
	if err := c.reconcileNodes(ctx); err != nil {
		c.log.Error(err, "Failed to reconcile nodes")
	}

	for {
		select {
		case <-ctx.Done():
			c.log.Info("Shutdown signal received, stopping controller")
			return nil
		case <-ticker.C:
			if err := c.reconcileNodes(ctx); err != nil {
				c.log.Error(err, "Failed to reconcile nodes")
			}
		}
	}
}

func (c *ProviderIDController) reconcileNodes(ctx context.Context) error {
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	c.log.Info("Reconciling nodes", "count", len(nodes.Items))

	for i := range nodes.Items {
		node := &nodes.Items[i]

		// Skip if providerID is already set
		if node.Spec.ProviderID != "" {
			continue
		}

		c.log.Info("Processing node", "node", node.Name)

		if err := c.setProviderID(ctx, node); err != nil {
			c.log.Error(err, "Failed to set providerID", "node", node.Name)
			continue
		}

		c.log.Info("Successfully set providerID", "node", node.Name, "providerID", node.Spec.ProviderID)
	}

	return nil
}

func (c *ProviderIDController) setProviderID(ctx context.Context, node *corev1.Node) error {
	instanceName := extractInstanceName(node.Name)

	c.log.Info("Processing node for providerID", "nodeName", node.Name, "extractedInstanceName", instanceName)

	// Validate the extracted instance name
	if err := validateInstanceName(instanceName); err != nil {
		return fmt.Errorf("invalid instance name extracted from node %s: %w", node.Name, err)
	}

	// Try to get zone from node labels first
	zone, hasZoneLabel := node.Labels[zoneLabel]

	var instance *compute.Instance
	var err error

	if hasZoneLabel {
		// If zone label exists, use it directly
		c.log.Info("Using zone from node label", "instanceName", instanceName, "zone", zone)
		instance, err = c.getInstance(ctx, zone, instanceName)
		if err != nil {
			return fmt.Errorf("failed to get instance: %w", err)
		}
	} else {
		// Try to extract zone from node name
		zoneFromName := extractZoneFromNodeName(node.Name)

		if zoneFromName != "" {
			// Try the extracted zone first
			c.log.Info("Trying zone extracted from node name", "instanceName", instanceName, "zone", zoneFromName)
			instance, err = c.getInstance(ctx, zoneFromName, instanceName)
			if err != nil && !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "notFound") {
				return fmt.Errorf("failed to get instance from extracted zone: %w", err)
			}
			if instance != nil {
				c.log.Info("Found instance in extracted zone", "instanceName", instanceName, "zone", zoneFromName)
				zone = zoneFromName
			} else {
				c.log.Info("Instance not found in extracted zone", "instanceName", instanceName, "zone", zoneFromName)
			}
		}

		// If not found with extracted zone, fall back to searching all zones
		if instance == nil {
			c.log.Info("Searching for instance across all zones in region", "instanceName", instanceName, "region", c.region)
			instance, zone, err = c.findInstanceInRegion(ctx, instanceName)
			if err != nil {
				return fmt.Errorf("failed to find instance in region: %w", err)
			}
		}
	}

	if instance == nil {
		return fmt.Errorf("instance %s not found", instanceName)
	}

	// Construct providerID: gce://PROJECT/ZONE/INSTANCE
	providerID := fmt.Sprintf("gce://%s/%s/%s", c.project, zone, instanceName)

	// Update node spec
	node.Spec.ProviderID = providerID

	// Set zone label if not already set
	if !hasZoneLabel {
		if node.Labels == nil {
			node.Labels = make(map[string]string)
		}
		node.Labels[zoneLabel] = zone
		c.log.Info("Setting zone label on node", "node", instanceName, "zone", zone)
	}

	// Remove cloud taint if present
	c.removeCloudTaint(node)

	// Update the node
	_, err = c.clientset.CoreV1().Nodes().Update(ctx, node, metav1.UpdateOptions{})
	if err != nil {
		if apierrors.IsConflict(err) {
			// Retry on conflict
			return c.setProviderID(ctx, node)
		}
		return fmt.Errorf("failed to update node: %w", err)
	}

	return nil
}

func (c *ProviderIDController) getInstance(ctx context.Context, zone, instanceName string) (*compute.Instance, error) {
	instance, err := c.gcpClient.Instances.Get(c.project, zone, instanceName).Context(ctx).Do()
	if err != nil {
		// Check if it's a 404 (instance not found)
		if strings.Contains(err.Error(), "404") || strings.Contains(err.Error(), "notFound") {
			return nil, nil
		}
		return nil, err
	}

	return instance, nil
}

// findInstanceInRegion searches for an instance across all zones in the configured region
func (c *ProviderIDController) findInstanceInRegion(ctx context.Context, instanceName string) (*compute.Instance, string, error) {
	// List all zones in the region
	zones, err := c.gcpClient.Zones.List(c.project).Filter(fmt.Sprintf("name eq %s-.*", c.region)).Context(ctx).Do()
	if err != nil {
		return nil, "", fmt.Errorf("failed to list zones: %w", err)
	}

	c.log.Info("Searching zones in region", "instanceName", instanceName, "region", c.region, "zoneCount", len(zones.Items))

	// Search for the instance in each zone
	for _, zone := range zones.Items {
		c.log.Info("Checking zone for instance", "instanceName", instanceName, "zone", zone.Name)
		instance, err := c.getInstance(ctx, zone.Name, instanceName)
		if err != nil {
			return nil, "", fmt.Errorf("failed to get instance from zone %s: %w", zone.Name, err)
		}
		if instance != nil {
			c.log.Info("Found instance in zone", "instanceName", instanceName, "zone", zone.Name)
			return instance, zone.Name, nil
		}
	}

	return nil, "", nil
}

func (c *ProviderIDController) removeCloudTaint(node *corev1.Node) {
	newTaints := []corev1.Taint{}
	for _, taint := range node.Spec.Taints {
		if taint.Key != cloudTaintKey {
			newTaints = append(newTaints, taint)
		}
	}
	node.Spec.Taints = newTaints
}
