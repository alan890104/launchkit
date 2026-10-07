package provider

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	runv1 "google.golang.org/api/run/v1"
)

// Compile-time interface guard.
var _ DomainMapper = (*CloudRun)(nil)

// MapDomain creates a Cloud Run domain mapping for the given service.
// Returns DNS records the user must add (typically a CNAME to ghs.googlehosted.com).
func (cr *CloudRun) MapDomain(ctx context.Context, opts MapDomainOpts) (*MapDomainResult, error) {
	svc, err := runv1.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create run v1 client: %w", err)
	}

	parent := "namespaces/" + cr.project
	mapping := &runv1.DomainMapping{
		ApiVersion: "domains.cloudrun.com/v1",
		Kind:       "DomainMapping",
		Metadata: &runv1.ObjectMeta{
			Name:      opts.Domain,
			Namespace: cr.project,
		},
		Spec: &runv1.DomainMappingSpec{
			RouteName:       opts.ServiceName,
			CertificateMode: "AUTOMATIC",
		},
	}

	result, err := svc.Namespaces.Domainmappings.Create(parent, mapping).Context(ctx).Do()
	if err != nil {
		// If mapping already exists, fetch it instead of erroring
		if isAlreadyExists(err) {
			return cr.getDomainRecords(ctx, svc, opts.Domain)
		}
		return nil, fmt.Errorf("create domain mapping: %w", err)
	}

	return domainMappingToResult(result), nil
}

// UnmapDomain removes a Cloud Run domain mapping.
func (cr *CloudRun) UnmapDomain(ctx context.Context, opts UnmapDomainOpts) error {
	svc, err := runv1.NewService(ctx)
	if err != nil {
		return fmt.Errorf("create run v1 client: %w", err)
	}

	name := "namespaces/" + cr.project + "/domainmappings/" + opts.Domain
	_, err = svc.Namespaces.Domainmappings.Delete(name).Context(ctx).Do()
	if err != nil {
		return fmt.Errorf("delete domain mapping: %w", err)
	}
	return nil
}

// VerifyDomain checks the current status of a Cloud Run domain mapping.
func (cr *CloudRun) VerifyDomain(ctx context.Context, opts VerifyDomainOpts) (*DomainStatus, error) {
	svc, err := runv1.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("create run v1 client: %w", err)
	}

	name := "namespaces/" + cr.project + "/domainmappings/" + opts.Domain
	mapping, err := svc.Namespaces.Domainmappings.Get(name).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get domain mapping: %w", err)
	}

	status := &DomainStatus{
		SSLStatus: "pending",
	}

	for _, cond := range mapping.Status.Conditions {
		switch cond.Type {
		case "Ready":
			if cond.Status == "True" {
				status.DNSVerified = true
				status.OwnerVerified = true
			}
		case "CertificateProvisioned":
			switch cond.Status {
			case "True":
				status.SSLStatus = "active"
			case "Unknown":
				status.SSLStatus = "provisioning"
			}
		case "DomainMappingConditionReady":
			if cond.Status == "True" {
				status.DNSVerified = true
			}
		}
	}

	return status, nil
}

func (cr *CloudRun) getDomainRecords(ctx context.Context, svc *runv1.APIService, domain string) (*MapDomainResult, error) {
	name := "namespaces/" + cr.project + "/domainmappings/" + domain
	mapping, err := svc.Namespaces.Domainmappings.Get(name).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("get existing domain mapping: %w", err)
	}
	return domainMappingToResult(mapping), nil
}

func domainMappingToResult(mapping *runv1.DomainMapping) *MapDomainResult {
	var records []DNSRecord
	for _, rr := range mapping.Status.ResourceRecords {
		records = append(records, DNSRecord{
			Type:  rr.Type,
			Name:  rr.Name,
			Value: rr.Rrdata,
		})
	}

	// If no records returned yet (pending), provide the default CNAME
	if len(records) == 0 && mapping.Metadata != nil {
		records = append(records, DNSRecord{
			Type:  "CNAME",
			Name:  mapping.Metadata.Name,
			Value: "ghs.googlehosted.com.",
		})
	}

	return &MapDomainResult{Records: records}
}

func isAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "409") ||
		strings.Contains(errStr, "already exists") ||
		strings.Contains(errStr, fmt.Sprintf("%d", http.StatusConflict))
}
