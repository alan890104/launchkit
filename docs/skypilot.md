# SkyPilot Architecture Reference — Design Lessons for LaunchKit

> **Purpose**: stand on the shoulders of giants. SkyPilot is an open-source infrastructure orchestration system from the UC Berkeley Sky Computing Lab (~9,700 stars, three top-conference papers: NSDI 2023/2024, EuroSys 2025), supporting 30+ clouds, K8s, and Slurm. This document extracts the design patterns and code references most valuable to LaunchKit, covering every resource provisioning approach beyond VMs.
>
> **Scope**: not just GPUs — CPU build VMs, serverless compute, storage, autoscaling, job scheduling, cost optimization, and multi-tenancy are all covered.

---

## Table of Contents

1. [Core Architecture Overview](#1-core-architecture-overview)
2. [Resource Abstraction and Catalog System](#2-resource-abstraction-and-catalog-system)
3. [Price Optimizer](#3-price-optimizer)
4. [Provisioner Three-Layer Architecture and Failover](#4-provisioner-three-layer-architecture-and-failover)
5. [GCP-Specific Implementation](#5-gcp-specific-implementation)
6. [Spot Instance Management and Preemption Recovery](#6-spot-instance-management-and-preemption-recovery)
7. [Autoscaling System (Sky Serve)](#7-autoscaling-system-sky-serve)
8. [Autostop and Idle Resource Reclamation](#8-autostop-and-idle-resource-reclamation)
9. [Storage and Data Transfer](#9-storage-and-data-transfer)
10. [Job Scheduling and Task Management](#10-job-scheduling-and-task-management)
11. [Multi-Tenancy and RBAC](#11-multi-tenancy-and-rbac)
12. [Kubernetes Integration Patterns](#12-kubernetes-integration-patterns)
13. [LaunchKit Use Cases](#13-launchkit-use-cases)
14. [Industry Case Studies and Cost Data](#14-industry-case-studies-and-cost-data)
15. [Academic Paper Index](#15-academic-paper-index)

---

## 1. Core Architecture Overview

### SkyPilot's Three Core Abstractions

```
┌──────────────────────────────────────────────────────────────────┐
│                        SkyPilot Architecture                     │
│                                                                  │
│  ┌──────────────┐  ┌──────────────┐  ┌───────────────────────┐  │
│  │   Clusters   │  │ Managed Jobs │  │     Sky Serve         │  │
│  │              │  │              │  │                       │  │
│  │ Interactive  │  │ Auto-recover │  │ Multi-replica serving │  │
│  │ dev VMs/pods │  │ spot jobs    │  │ with autoscaling      │  │
│  └──────┬───────┘  └──────┬───────┘  └───────────┬───────────┘  │
│         │                 │                       │              │
│  ┌──────┴─────────────────┴───────────────────────┴──────────┐  │
│  │                      Optimizer                             │  │
│  │  ILP solver: 16K+ candidates → cheapest placement         │  │
│  │  Cross-cloud arbitrage · Spot/on-demand · Region/zone     │  │
│  └──────────────────────────┬────────────────────────────────┘  │
│                              │                                   │
│  ┌───────────────────────────┴───────────────────────────────┐  │
│  │                     Provisioner                            │  │
│  │  Zone → Region → Cloud failover · Blocked resource track  │  │
│  │  30+ cloud backends · K8s · Slurm                         │  │
│  └───────────────────────────────────────────────────────────┘  │
└──────────────────────────────────────────────────────────────────┘
```

### Mapping to LaunchKit

| SkyPilot concept | LaunchKit counterpart | Lesson |
|---|---|---|
| Clusters | BuildKit VM, Cloud Run instances | VM lifecycle management |
| Managed Jobs | Build jobs, GPU training jobs | Preemption recovery, automatic retry |
| Sky Serve | Cloud Run serving | Autoscaling patterns |
| Optimizer | Resource selection in the Plan Engine | Best-price algorithm |
| Provisioner | The Orchestrator's provision workers | Failover mechanism |
| Catalog | Internal instance pricing DB | Pricing data management |

---

## 2. Resource Abstraction and Catalog System

### 2.1 Resource Class — Immutable Resource Spec

**File**: `sky/resources.py:129-427`

SkyPilot's core design: all resource requirements are expressed as one immutable object, supporting exact specification or flexible matching.

```python
class Resources:
    """Immutable compute requirements — used for requests, filters, and billing."""
    _VERSION = 32

    def __init__(
        self,
        cloud: Optional[clouds.Cloud] = None,         # target cloud (AWS, GCP, ...)
        instance_type: Optional[str] = None,           # exact machine type (e2-standard-4)
        cpus: Union[None, int, float, str] = None,     # CPU requirement ('8' or '8+')
        memory: Union[None, int, float, str] = None,   # memory ('32' or '4x')
        accelerators: Union[None, str, Dict] = None,   # GPU/TPU ('V100:4')
        use_spot: Optional[bool] = None,               # Spot / Preemptible
        region: Optional[str] = None,
        zone: Optional[str] = None,
        disk_size: Optional[Union[str, int]] = None,
        disk_tier: Optional[str] = None,               # pd-ssd, pd-balanced, ...
        max_hourly_cost: Optional[float] = None,       # price cap
        ports: Optional[Union[int, str, List[str]]] = None,
        labels: Optional[Dict[str, str]] = None,
        autostop: Union[bool, int, str, Dict, None] = None,
        priority: Optional[int] = None,
        # ... 23+ parameters
    ):
```

**Key design pattern — Immutable Copy**:
```python
# Modifying creates a new object and leaves the original unchanged (safe to operate on in parallel inside the optimizer)
new_resources = resources.copy(region="us-east-1", zone="us-east-1a")
```

**Lesson for LaunchKit**: the `config JSON` in our `services` table can use a similar structure:

```go
// LaunchKit: unified resource spec (Go struct)
type ResourceSpec struct {
    Cloud        string  `json:"cloud"`         // "gcp"
    InstanceType string  `json:"instance_type"` // "e2-standard-4" (optional)
    CPUs         string  `json:"cpus"`          // "4+" (at-least matching)
    Memory       string  `json:"memory"`        // "16" or "4x" (per-CPU ratio)
    Accelerators string  `json:"accelerators"`  // "L4:1" (optional)
    UseSpot      bool    `json:"use_spot"`
    Region       string  `json:"region"`
    DiskSizeGB   int     `json:"disk_size_gb"`
    MaxHourlyCost *float64 `json:"max_hourly_cost"` // budget cap
}
```

### 2.2 Flexible Matching Syntax

**File**:`sky/catalog/common.py:431-479`

SkyPilot's CPU/Memory matching supports several syntaxes, which is very useful for LaunchKit's build resource selection:

```python
# exact match
cpus='8'        # exactly 8 vCPU
memory='32'     # exactly 32 GiB

# At-least match (most practical for build jobs)
cpus='4+'       # >= 4 vCPU → picks the cheapest machine type with >= 4 cores
memory='16+'    # >= 16 GiB

# ratio match
memory='4x'     # 4 GiB per vCPU (8-core = 32 GiB)
                # m-family: 4x, r-family: 8x, c-family: 2x

def _filter_with_cpus(df, cpus):
    """Filter instances by CPU requirements."""
    if cpus.endswith('+'):
        min_cpus = float(cpus[:-1])
        return df[df['vCPUs'] >= min_cpus]
    else:
        return df[df['vCPUs'] == float(cpus)]

def _filter_with_mem(df, memory):
    """Filter instances by memory requirements."""
    if memory.endswith('x'):
        ratio = float(memory[:-1])
        return df[df['MemoryGiB'] >= df['vCPUs'] * ratio]
    elif memory.endswith('+'):
        min_mem = float(memory[:-1])
        return df[df['MemoryGiB'] >= min_mem]
    else:
        return df[df['MemoryGiB'] == float(memory)]
```

**LaunchKit scenarios**:

| User need | Resource spec | Auto-selected result |
|---|---|---|
| Small Node.js build | `cpus='2+', memory='4+'` | `e2-small` ($0.017/hr) |
| Medium monorepo build | `cpus='4+', memory='8+'` | `e2-standard-4` ($0.134/hr) |
| Large Rust/Go build | `cpus='8+', memory='16+'` | `e2-standard-8` ($0.268/hr) |
| ML training | `accelerators='L4:1'` | `g2-standard-4` ($0.84/hr) |
| GPU inference | `accelerators='T4:1', use_spot=True` | `n1-standard-4 + T4` ($0.13/hr spot) |

### 2.3 Catalog System — Pricing Data Management

**File**:`sky/catalog/common.py:167-266`

```
~/.sky/catalogs/v{SCHEMA_VERSION}/{cloud}/vms.csv

CSV columns:
InstanceType | AcceleratorName | AcceleratorCount | vCPUs | MemoryGiB |
Price ($/hr) | SpotPrice ($/hr) | Region | AvailabilityZone
```

**LazyDataFrame — lazy loading + automatic updates**:

```python
class LazyDataFrame:
    """On-demand loading with auto-refresh."""
    _df: Optional[pd.DataFrame] = None
    _filename: str
    _update_if_stale_func: Callable  # check + update (every 7 hours)

    def __getattr__(self, name):
        if self._df is None:
            self._df = pd.read_csv(self._filename)  # only read on first use
        return getattr(self._df, name)
```

**Lesson for LaunchKit**: we do not need the catalog of all 30+ clouds, but we can maintain a GCP instance pricing table:

```go
// LaunchKit: slimmed-down pricing catalog (stored in Postgres or embedded in the Go binary)
type InstancePricing struct {
    InstanceType  string  // "e2-standard-4"
    VCPUs         int     // 4
    MemoryGiB     float64 // 16
    OnDemandPrice float64 // 0.134 $/hr
    SpotPrice     float64 // 0.040 $/hr (preemptible)
    Region        string  // "us-central1"
    Family        string  // "e2" (general), "n2" (standard), "c2" (compute)
}

// Daily cron updates from the GCP pricing API → refresh catalog
// Or embed a static table + quarterly updates (enough for Phase 1)
```

---

## 3. Price Optimizer

### 3.1 Core Algorithm

**File**:`sky/optimizer.py:109-142`

SkyPilot's Optimizer uses two algorithms:
- **DP (Dynamic Programming)**: linear pipeline (A → B → C), O(T×R²)
- **ILP (Integer Linear Programming)**: DAG structure, CBC solver (PuLP)

```python
@staticmethod
def optimize(dag: 'dag_lib.Dag',
             minimize: OptimizeTarget = OptimizeTarget.COST,
             blocked_resources: Optional[Iterable[Resources]] = None,
             quiet: bool = False) -> 'dag_lib.Dag':
    """Find the best execution plan for the given DAG.

    Mutates every node in 'dag' by setting node.best_resources.
    """
    _check_specified_clouds(dag)
    Optimizer._add_dummy_source_sink_nodes(dag)
    try:
        Optimizer._optimize_dag(
            dag=dag,
            minimize_cost=minimize == OptimizeTarget.COST,
            blocked_resources=blocked_resources,
            quiet=quiet)
    finally:
        Optimizer._remove_dummy_source_sink_nodes(dag)
    return dag
```

### 3.2 Cost Estimation

**File**:`sky/optimizer.py:239-254`, `sky/resources.py:1685-1698`

```python
def _estimate_nodes_cost_or_time(topo_order, minimize_cost=True, ...):
    """Estimates cost of each task-resource mapping.

    estimated_cost = task.num_nodes * resources.get_cost(runtime)
    """
    node_to_cost_map: Dict[Task, Dict[Resources, float]] = defaultdict(dict)
    # ...

# Resources.get_cost():
def get_cost(self, seconds: float) -> float:
    """Returns cost in USD for the runtime in seconds."""
    hours = seconds / 3600
    hourly_cost = self.cloud.instance_type_to_hourly_cost(
        self._instance_type, self.use_spot, self._region, self._zone)
    if self.accelerators is not None:
        hourly_cost += self.cloud.accelerators_to_hourly_cost(
            self.accelerators, self.use_spot, self._region, self._zone)
    return float(hourly_cost * hours)
```

### 3.3 Catalog Lookup — Find the Cheapest Instance for the Requirements

**File**:`sky/catalog/common.py:641-694`

This is the most central function in the whole system — it maps abstract requirements to a concrete instance type:

```python
def get_instance_type_for_accelerator_impl(
    df: pd.DataFrame,
    acc_name: str,
    acc_count: Union[int, float],
    cpus: Optional[str] = None,
    memory: Optional[str] = None,
    use_spot: bool = False,
    region: Optional[str] = None,
    zone: Optional[str] = None,
    max_hourly_cost: Optional[float] = None,
) -> Tuple[Optional[List[str]], List[str]]:
    """Filter instance types by requirements, return sorted by price."""

    # Step 1: exact accelerator match
    result = df[
        (df['AcceleratorName'].str.fullmatch(acc_name, case=False)) &
        (abs(df['AcceleratorCount'] - acc_count) <= 0.01)
    ]
    result = _filter_region_zone(result, region, zone)

    # Step 2: if there is no exact match, fuzzy search
    if result.empty:
        fuzzy_result = df[
            (df['AcceleratorName'].str.contains(acc_name, case=False)) &
            (df['AcceleratorCount'] >= acc_count)
        ]
        return (None, fuzzy_candidate_list)

    # Step 3: CPU / Memory filtering
    result = _filter_with_cpus(result, cpus)
    result = _filter_with_mem(result, memory)

    # Step 4: price filtering + sorting (cheapest first)
    price_str = 'SpotPrice' if use_spot else 'Price'
    if max_hourly_cost is not None:
        result = result[result[price_str] <= max_hourly_cost]
    result = result.sort_values(price_str, ascending=True)

    instance_types = list(result['InstanceType'].drop_duplicates())
    return (instance_types, [])  # ← cheapest first
```

### 3.4 ILP Formulation

**File**:`sky/optimizer.py:490-637`

```
Decision Variables:
  c[v][i] ∈ {0,1}  = task v uses the i-th resource option
  e[u][v][a][b] ∈ {0,1}  = task u uses resource a AND task v uses resource b

Objective (minimize):
  Σ_v  c[v]ᵀ · k[v]       (execution costs)
  + Σ_(u,v)  e[u][v]ᵀ · F[u][v]  (egress/data transfer costs)

Constraints:
  Σ_i c[v][i] == 1  ∀v    (each task is assigned exactly one resource)
  e[u][v] = c[u] ⊗ c[v]   (linearized outer product)

Solver: CBC (Coin-or-branch-and-cut) via PuLP
```

**Lesson for LaunchKit**: Phase 1 does not need an ILP solver, but a simplified version can be used:

```go
// LaunchKit: simplified resource selector
func SelectCheapestInstance(spec ResourceSpec) (InstancePricing, error) {
    candidates := catalog.Filter(CatalogFilter{
        MinCPUs:       spec.ParseCPUs(),
        MinMemoryGiB:  spec.ParseMemory(),
        Accelerator:   spec.Accelerators,
        Region:        spec.Region,
        MaxHourlyCost: spec.MaxHourlyCost,
    })

    if spec.UseSpot {
        sort.Slice(candidates, func(i, j int) bool {
            return candidates[i].SpotPrice < candidates[j].SpotPrice
        })
    } else {
        sort.Slice(candidates, func(i, j int) bool {
            return candidates[i].OnDemandPrice < candidates[j].OnDemandPrice
        })
    }

    if len(candidates) == 0 {
        return InstancePricing{}, ErrNoFeasibleInstance
    }
    return candidates[0], nil
}
```

---

## 4. Provisioner Three-Layer Architecture and Failover

### 4.1 Decorator-Based Routing

**File**:`sky/provision/__init__.py`

SkyPilot uses a decorator to dispatch automatically to the right cloud, with zero if-else:

```python
@_route_to_cloud_impl
def run_instances(provider_name, region, cluster_name, config, ...):
    raise NotImplementedError

# On the actual call:
provision.run_instances('gcp', ...)
# → automatically routed to sky.provision.gcp.run_instances()
```

Unified API:
```python
provision.bootstrap_instances()   # create auxiliary resources (VPC, firewall, ...)
provision.run_instances()         # start the VM
provision.wait_instances()        # wait until ready
provision.query_instances()       # query status
provision.stop_instances()        # stop (keeps the disks)
provision.terminate_instances()   # terminate (deletes everything)
provision.open_ports()            # open ports
provision.cleanup_ports()         # clean up firewall rules
```

**Lesson for LaunchKit**: our Provider interface can use the same pattern:

```go
// LaunchKit: Provider interface (corresponds to SkyPilot's provision routing)
type ComputeProvider interface {
    Provision(ctx context.Context, spec ResourceSpec) (*Instance, error)
    Stop(ctx context.Context, instanceID string) error
    Terminate(ctx context.Context, instanceID string) error
    Query(ctx context.Context, instanceID string) (InstanceStatus, error)
}

// registry
var providers = map[string]ComputeProvider{
    "cloud-run":  &CloudRunProvider{},
    "buildkit":   &BuildKitVMProvider{},
    "skypilot":   &SkyPilotGPUProvider{},
}
```

### 4.2 Three-Level Failover — Zone → Region → Cloud

**File**:`sky/backends/cloud_vm_ray_backend.py:736-910`

This is one of SkyPilot's most elegant designs:

```python
class RetryingVmProvisioner:
    """Zone → Region → Cloud cascading failover."""

    def __init__(self, ...):
        # track failed resource combinations to avoid retrying them
        self._blocked_resources: Set[Resources] = set()

    def _yield_zones(self, to_provision, num_nodes, cluster_name,
                     prev_cluster_status, prev_cluster_ever_up):
        """Yield zones in price order, automatically moving on to the next on failure."""

        # existing cluster → retry only in its original zone
        if prev_cluster_status is not None:
            zones = _get_previously_launched_zones()
            yield zones

            # if the cluster has ever been UP → do not fail over (protects data)
            if prev_cluster_ever_up:
                raise ResourcesUnavailableError(message, no_failover=True)

            # INIT state (never succeeded) → failover is allowed
            raise ResourcesUnavailableError(message)  # no_failover=False

        # new cluster → iterate over all zones (sorted by price)
        for zones in cloud.zones_provision_loop(
                region=to_provision.region,
                num_nodes=num_nodes,
                instance_type=to_provision.instance_type,
                accelerators=to_provision.accelerators,
                use_spot=to_provision.use_spot):
            yield zones

    def provision_with_retries(self, task, to_provision_config, ...):
        """Outer loop: when all zones fail → re-run the optimizer → pick a new region/cloud."""
        while True:
            try:
                for zones in self._yield_zones(...):
                    try:
                        return self._retry_zones(zones, ...)
                    except ResourcesUnavailableError:
                        self._blocked_resources.add(failed_resource)
                        continue  # try the next zone
            except ResourcesUnavailableError as e:
                if e.no_failover:
                    raise
                # re-run the optimizer, excluding blocked resources
                new_resources = Optimizer.optimize(
                    dag, blocked_resources=self._blocked_resources)
                continue  # retry with the new resources
```

**Timeout-Based Failover**:
```python
_NODES_LAUNCHING_PROGRESS_TIMEOUT = {
    clouds.AWS: 90,    # 90s without progress → failover
    clouds.GCP: 240,   # GCP is slower
    clouds.Lambda: 300,
}
```

**Lesson for LaunchKit**: both BuildKit VM and GPU provisioning can use this pattern:

```go
// LaunchKit: Simplified failover for build VMs
func ProvisionBuildVM(ctx context.Context, spec ResourceSpec) (*Instance, error) {
    blocked := make(map[string]bool)

    // zone list sorted by price
    zones := catalog.ZonesByPrice(spec.Region, spec)

    for _, zone := range zones {
        if blocked[zone] {
            continue
        }

        instance, err := gcpClient.CreateInstance(ctx, spec, zone)
        if err == nil {
            return instance, nil
        }

        if isQuotaOrCapacityError(err) {
            blocked[zone] = true
            continue // next zone
        }
        return nil, err // unrecoverable error
    }

    return nil, ErrAllZonesExhausted
}
```

### 4.3 Blocked Resources Tracking

```python
def _add_to_blocked_resources(blocked_set, failed_resource):
    """Record precisely which (cloud, region, zone, instance_type) combinations failed."""
    blocked_set.add(failed_resource)
    # the optimizer will later exclude these combinations
```

### 4.4 Feature Capability Declaration

**File**:`sky/clouds/cloud.py`

Every cloud declares the features it supports, so the provisioner can validate before attempting:

```python
class CloudImplementationFeatures(enum.Enum):
    STOP = 'stop'
    MULTI_NODE = 'multi_node'
    SPOT_INSTANCE = 'spot_instance'
    STORAGE_MOUNTING = 'storage_mounting'
    DOCKER_IMAGE = 'docker_image'
    OPEN_PORTS = 'open_ports'
    AUTO_TERMINATE = 'auto_terminate'
    CUSTOM_DISK_TIER = 'custom_disk_tier'
    # ... 20+ features

# each cloud reports the features it does not support:
class GCP(Cloud):
    def _unsupported_features_for_resources(self, resources):
        unsupported = {}
        if is_tpu(resources):
            unsupported[Features.STOP] = 'TPU VMs cannot be stopped'
        if is_mig(resources):
            unsupported[Features.SPOT_INSTANCE] = 'MIG does not support spot'
        return unsupported
```

---

## 5. GCP-Specific Implementation

### 5.1 Instance Type System

**File**:`sky/provision/gcp/instance_utils.py`

GCP has three instance handlers:

```python
class GCPNodeType(enum.Enum):
    COMPUTE = 'compute'    # standard VM (n1, n2, e2, c2, ...)
    MIG = 'mig'            # Managed Instance Groups (DWS)
    TPU = 'tpu'            # TPU VMs

# routing logic:
def get_node_type(config):
    if has_acceleratorType and no_machineType:
        return GCPNodeType.TPU
    if has_machineType and has_guestAccelerators and has_MIG_CONFIG:
        return GCPNodeType.MIG
    return GCPNodeType.COMPUTE  # most common
```

### 5.2 GCP Instance Creation (actual API calls)

**File**:`sky/provision/gcp/instance_utils.py:644-893`

```python
def create_instances(cls, cluster_name, project_id, zone, node_config,
                     labels, count, total_count, include_head_node):
    config = copy.deepcopy(node_config)
    names = [_generate_node_name(cluster_name, 'compute', is_head=i==0)
             for i in range(count)]

    labels = {
        **config.get('labels', {}),
        **labels,
        TAG_RAY_CLUSTER_NAME: cluster_name,
        TAG_SKYPILOT_CLUSTER_NAME: cluster_name,
    }
    config['labels'] = labels

    # Use reservations first (prepaid = $0)
    if 'reservationAffinity' in config:
        reservations = gcp.get_reservations_available_resources(
            config['machineType'], region=region, zone=zone,
            specific_reservations=specific_reservations)
        # sort by available count, prefer reservations
        for reservation, count in sorted_reservations:
            errors = cls._create_instances(names[:count], project_id, zone,
                                          config_with_reservation)
            # ...

    # standard creation (bulkInsert or insert)
    if cls._use_bulk_insert(config):
        operations = cls._bulk_insert(names, project_id, zone, config)
    else:
        operations = cls._insert(names, project_id, zone, config)

@classmethod
def _insert(cls, names, project_id, zone, config):
    """Single instance creation via GCP Compute Engine API."""
    request = cls.load_resource().instances().insert(
        project=project_id,
        zone=zone,
        body=body,
    )
    operation = request.execute(num_retries=GCP_MAX_RETRIES)
```

### 5.3 GCP Default Instance Families

**File**:`sky/catalog/gcp_catalog.py`

```python
# CPU-only workloads:
_DEFAULT_INSTANCE_FAMILY = [
    'n2-standard',    # 4 GiB/vCPU — general purpose
    'n2-highmem',     # 8 GiB/vCPU — memory-intensive
    'n2-highcpu',     # 1 GiB/vCPU — compute-intensive
    'n4-standard',    # latest generation
    'n4-highcpu',
    'n4-highmem',
]

# GPU host machines:
_DEFAULT_HOST_VM_FAMILY = ('n1-standard', 'n1-highmem', 'n1-highcpu')

# GPU → Instance Type fixed mapping:
_ACC_INSTANCE_TYPE_DICTS = {
    'A100':     {1: ['a2-highgpu-1g'], 4: ['a2-highgpu-4g'], 8: ['a2-highgpu-8g']},
    'A100-80GB': {1: ['a2-ultragpu-1g'], 8: ['a2-ultragpu-8g']},
    'L4':       {1: ['g2-standard-4', 'g2-standard-8', ...], 8: ['g2-standard-96']},
    'H100':     {8: ['a3-highgpu-8g', 'a3-megagpu-8g']},
    'B200':     {8: ['a4-highgpu-8g']},
}
```

### 5.4 GCP Image Selection

```python
_DEFAULT_CPU_IMAGE_ID = 'skypilot:custom-cpu-ubuntu-2204'
_DEFAULT_GPU_IMAGE_ID = 'skypilot:custom-gpu-ubuntu-2204'
_DEFAULT_GPU_K80_IMAGE_ID = 'skypilot:k80-debian-10'
_DEFAULT_GPU_DIRECT_IMAGE_ID = 'skypilot:gpu-direct-cos'  # GPUDirect + COS

# automatic selection:
def make_deploy_resources_variables(resources):
    if resources.accelerators:
        acc = list(accelerators.keys())[0]
        if enable_gpu_direct:
            image_id = _DEFAULT_GPU_DIRECT_IMAGE_ID
        elif acc == 'K80':
            image_id = _DEFAULT_GPU_K80_IMAGE_ID
        else:
            image_id = _DEFAULT_GPU_IMAGE_ID
    else:
        image_id = _DEFAULT_CPU_IMAGE_ID
```

### 5.5 GCP Disk Tier Mapping

```python
# standard series:
'pd-extreme'    # highest IOPS
'pd-ssd'        # SSD
'pd-balanced'   # best price/performance
'pd-standard'   # HDD

# a3/a4 series (GPU) → hyperdisk is used automatically:
if instance_series in ('a3', 'a4'):
    disk_type = 'hyperdisk-balanced'
```

### 5.6 GCP Authentication

**File**:`sky/authentication.py`, `sky/clouds/gcp.py:845-1051`

```python
def check_credentials(self):
    """GCP credential validation flow:
    1. check google-api-python-client
    2. check gcloud CLI
    3. verify ~/.config/gcloud/access_tokens.db
    4. verify application default credentials
    5. get the project ID
    6. IAM permission test
    7. enable the required APIs: compute, cloudresourcemanager, iam, tpu(optional)
    """

# Credential files synced to remote VM:
_CREDENTIAL_FILES = [
    'credentials.db', 'access_tokens.db', 'configurations',
    'legacy_credentials', 'active_config',
]
```

---

## 6. Spot Instance Management and Preemption Recovery

### 6.1 Recovery Strategy Pattern

**File**:`sky/jobs/recovery_strategy.py`

The core design of SkyPilot's managed jobs — automatically recovering from spot preemptions:

```python
class StrategyExecutor:
    """Handle launching, recovery and termination of managed job clusters."""

    def __init__(self, cluster_name, backend, task,
                 max_restarts_on_errors, job_id, task_id,
                 recover_on_exit_codes=None):
        self.cluster_name = cluster_name
        self.max_restarts_on_errors = max_restarts_on_errors
        self.recover_on_exit_codes = recover_on_exit_codes

    async def recover(self):
        """Spot preemption recovery:
        1. Detect the preemption
        2. Tear down the failed cluster
        3. Search for available spot capacity across regions/clouds
        4. Create a new VM
        5. Resume the job (from a checkpoint if there is one)
        """
```

### 6.2 Preemption Detection

**File**:`sky/provision/gcp/instance.py:107-115`

```python
def query_instances(cluster_name_on_cloud, provider_config):
    """Query and handle preempted instances."""
    statuses = handler.query_status(...)

    # GCP does not automatically clean up preempted TPU VMs
    if handler == GCPTPUVMInstance:
        all_preempted = all(s == 'PREEMPTED' for s in raw_statuses.values())
        if all_preempted:
            logger.info(f'Terminating preempted TPU VM cluster {cluster_name}')
            terminate_instances(cluster_name, provider_config)

    return statuses
```

### 6.3 Recovery Strategy Types

| Failure type | Behavior |
|---|---|
| Spot preemption / hardware failure | Auto teardown + re-provision in a new region |
| Resource unavailable | Retry indefinitely, across regions/clouds |
| User program error | `max_restarts_on_errors` controls the retry count |
| Configuration error | Marked FAILED_PRECHECKS, no retry |

### 6.4 Checkpoint-Based Recovery

```yaml
# SkyPilot task YAML with spot recovery:
file_mounts:
  /checkpoint:
    name: my-checkpoint-bucket
    mode: MOUNT_CACHED  # Write-back cache, flush on exit

resources:
  use_spot: true
  # the application writes checkpoints to /checkpoint/ periodically
  # on recovery, continue from the latest checkpoint
```

### 6.5 Academic Backing: NSDI 2024 "Can't Be Late"

SkyPilot's spot strategy is backed by a peer-reviewed paper (NSDI 2024). The algorithm uses a **time-sliced greedy approach**:

1. **Thrifty Rule**: do not schedule a job that has no remaining work
2. **Safety Net Rule**: if the spot interruption risk may cause a missed deadline → migrate to on-demand
3. **Exploitation Rule**: once spot is in use, keep using it until it becomes unavailable

**Lesson for LaunchKit**: build jobs are stateless, so they naturally fit spot:

```go
// LaunchKit: build jobs use spot instances
type BuildConfig struct {
    // ...
    UseSpot     bool          `json:"use_spot"`     // default: true for builds
    SpotTimeout time.Duration `json:"spot_timeout"` // how long to wait before failover after preemption
    FallbackToOnDemand bool   `json:"fallback_to_on_demand"` // when spot is unavailable
}

// Build jobs naturally fit spot:
// 1. Stateless — just rerun on failure, the build cache lives on the BuildKit VM SSD
// 2. Short-lived — a typical build is < 5 min, so preemption probability is low
// 3. Saves 60-80% — e2-standard-4 on-demand $0.134/hr vs preemptible $0.040/hr
```

---

## 7. Autoscaling System (Sky Serve)

### 7.1 Autoscaler Architecture

**File**:`sky/serve/autoscalers.py`

SkyPilot implements four autoscalers, selected with a factory pattern:

```python
class Autoscaler:
    """Base autoscaler interface."""

    @classmethod
    def from_spec(cls, service_name, spec) -> 'Autoscaler':
        """Factory: pick the autoscaler according to the spec."""
        if spec.pool:
            return QueueLengthAutoscaler(service_name, spec)
        elif spec.use_ondemand_fallback:
            return FallbackRequestRateAutoscaler(service_name, spec)
        elif isinstance(spec.target_qps_per_replica, dict):
            return InstanceAwareRequestRateAutoscaler(service_name, spec)
        else:
            return RequestRateAutoscaler(service_name, spec)

    def generate_scaling_decisions(self, replica_infos, active_versions):
        """Core API: return SCALE_UP or SCALE_DOWN decisions."""
        pass
```

### 7.2 Hysteresis — Preventing Oscillation

**File**:`sky/serve/autoscalers.py:372-456`

This is the key design of production-grade autoscaling — asymmetric delays prevent resource oscillation:

```python
class _AutoscalerWithHysteresis(Autoscaler):
    """Damped scaling to prevent oscillation."""

    def _setup_thresholds(self, spec):
        # decision interval: 30 seconds
        decision_interval = 30  # AUTOSCALER_DEFAULT_DECISION_INTERVAL_SECONDS

        # Scale-up delay: 300 seconds → requires 10 consecutive decisions
        upscale_delay = spec.upscale_delay_seconds or 300
        self.scale_up_threshold = int(upscale_delay / decision_interval)  # 10

        # Scale-down delay: 600 seconds → requires 20 consecutive decisions (more conservative)
        downscale_delay = spec.downscale_delay_seconds or 600
        self.scale_down_threshold = int(downscale_delay / decision_interval)  # 20

    def _set_target_num_replicas_with_hysteresis(self):
        target = self._calculate_target_num_replicas()

        # respond immediately when there are zero replicas (the service is down)
        if self.target_num_replicas == 0:
            self.target_num_replicas = target
            return

        if target > self.target_num_replicas:
            self.upscale_counter += 1
            self.downscale_counter = 0  # reset the opposite-direction counter
            if self.upscale_counter >= self.scale_up_threshold:
                self.upscale_counter = 0
                self.target_num_replicas = target
        elif target < self.target_num_replicas:
            self.downscale_counter += 1
            self.upscale_counter = 0
            if self.downscale_counter >= self.scale_down_threshold:
                self.downscale_counter = 0
                self.target_num_replicas = target
        else:
            self.upscale_counter = self.downscale_counter = 0
```

**Why asymmetric?**
- Scale-up is fast (5 minutes): avoids degrading the user experience
- Scale-down is slow (10 minutes): avoids the cold-start cost of repeatedly creating/destroying VMs

### 7.3 QPS-Based Autoscaler

```python
class RequestRateAutoscaler(_AutoscalerWithHysteresis):
    """Scale based on requests per second per replica."""

    def _calculate_target_num_replicas(self) -> int:
        # formula: ceil(current_qps / target_qps_per_replica)
        num_requests_per_second = len(self.request_timestamps) / self.qps_window_size
        target = math.ceil(num_requests_per_second / self.target_qps_per_replica)
        return self._clip_target_num_replicas(target)

    def _clip_target_num_replicas(self, target):
        return max(self.min_replicas, min(target, self.max_replicas))
```

### 7.4 Queue Length Autoscaler (for Batch / Pool)

```python
class QueueLengthAutoscaler(_AutoscalerWithHysteresis):
    """Scale based on pending jobs in queue.

    For batch workloads (such as a build queue) — each worker handles one job,
    and the queue depth determines how many workers are needed.
    """

    def _calculate_target_num_replicas(self) -> int:
        pending_jobs = self._get_queue_length()
        running_jobs = self._get_running_count()
        target = pending_jobs + running_jobs  # one worker per job
        return self._clip_target_num_replicas(target)
```

### 7.5 Load Balancing Policies

**File**:`sky/serve/load_balancing_policies.py`

```python
class RoundRobinPolicy(LoadBalancingPolicy, name='round_robin'):
    def _select_replica(self, request):
        url = self.ready_replicas[self.index]
        self.index = (self.index + 1) % len(self.ready_replicas)
        return url

class LeastLoadPolicy(LoadBalancingPolicy, name='least_load', default=True):
    """Track in-flight requests per replica — pick the least busy."""
    def _select_replica(self, request):
        return min(self.ready_replicas,
                   key=lambda r: self.load_map.get(r, 0))

    def pre_execute_hook(self, replica_url, request):
        self.load_map[replica_url] += 1

    def post_execute_hook(self, replica_url, request):
        self.load_map[replica_url] -= 1
```

### 7.6 Replica Lifecycle Management

**File**:`sky/serve/replica_managers.py`

```python
class SkyPilotReplicaManager(ReplicaManager):
    """Thread-pool based replica lifecycle management."""

    def __init__(self, service_name, spec, version):
        # thread pools for parallel operations
        self._launch_thread_pool = ThreadSafeDict()  # replica_id → thread
        self._down_thread_pool = ThreadSafeDict()

        # background daemon threads
        threading.Thread(target=self._thread_pool_refresher).start()
        threading.Thread(target=self._job_status_fetcher).start()
        threading.Thread(target=self._replica_prober).start()

        # recover interrupted operations
        self._recover_replica_operations()

    def _recover_replica_operations(self):
        """Restart interrupted operations on startup.
        Priority order: PROVISIONING > PENDING > SHUTTING_DOWN
        """
        # recover replicas that were being created
        for replica in get_replicas_at_status(PROVISIONING):
            self._launch_replica(replica.id)
        for replica in get_replicas_at_status(PENDING):
            self._launch_replica(replica.id)
        # recover replicas that were shutting down
        for replica in get_replicas_at_status(SHUTTING_DOWN):
            self._terminate_replica(replica.id)

    def scale_up(self, resources_override=None):
        """Add new replica (called by autoscaler)."""
        self._launch_replica(self._next_replica_id, resources_override)
        self._next_replica_id += 1
```

**Lesson for LaunchKit**: Cloud Run has built-in autoscaling, but GPU/BuildKit VMs must be managed by ourselves:

```go
// LaunchKit: BuildKit VM pool autoscaling
type BuildKitPoolAutoscaler struct {
    MinVMs            int           // 1 (always-on primary)
    MaxVMs            int           // 10
    TargetBuildsPerVM int           // 2 (concurrent builds per VM)
    ScaleUpDelay      time.Duration // 60s (builds are time-sensitive)
    ScaleDownDelay    time.Duration // 600s (avoid churn)
    upscaleCounter    int
    downscaleCounter  int
}

func (a *BuildKitPoolAutoscaler) Evaluate(queueDepth, activeBuilds int) Decision {
    target := (queueDepth + activeBuilds + a.TargetBuildsPerVM - 1) / a.TargetBuildsPerVM
    target = clamp(target, a.MinVMs, a.MaxVMs)

    if target > a.currentVMs {
        a.upscaleCounter++
        a.downscaleCounter = 0
        if a.upscaleCounter >= a.scaleUpThreshold() {
            return ScaleUp(target - a.currentVMs)
        }
    } else if target < a.currentVMs {
        a.downscaleCounter++
        a.upscaleCounter = 0
        if a.downscaleCounter >= a.scaleDownThreshold() {
            return ScaleDown(a.currentVMs - target)
        }
    }
    return NoOp
}
```

---

## 8. Autostop and Idle Resource Reclamation

### 8.1 Autostop Mechanism

**File**:`sky/skylet/autostop_lib.py`

SkyPilot runs a skylet daemon on every VM that monitors the idle state:

```python
# settings are stored in a local SQLite on the VM
_AUTOSTOP_CONFIG_KEY = 'autostop_config'
_AUTOSTOP_LAST_ACTIVE_TIME = 'autostop_last_active_time'
_AUTOSTOP_INDICATOR = 'autostop_indicator'  # Boot time (detects reboots)

class AutostopConfig:
    autostop_idle_minutes: int    # act after this many idle minutes
    down: bool                    # True=terminate, False=stop
    hook_commands: List[str]      # hook run before stopping (e.g., checkpoint)
    hook_timeout: int             # hook timeout (default: 3600s)

def set_last_active_time():
    """Any job activity updates last_active_time."""
    configs.set_config(_AUTOSTOP_LAST_ACTIVE_TIME, str(time.time()))

def is_idle():
    """Check whether the idle time has exceeded the configured limit."""
    last_active = get_last_active_time()
    idle_minutes = (time.time() - last_active) / 60
    return idle_minutes >= config.autostop_idle_minutes
```

**Lesson for LaunchKit**: autostop for the BuildKit VM:

```go
// LaunchKit: BuildKit VM idle detection
const (
    BuildKitIdleTimeout = 30 * time.Minute  // 30 min without builds → stop
    BuildKitDownTimeout = 4 * time.Hour     // 4 hr without builds → terminate
)

func (m *BuildKitManager) monitorIdle() {
    for {
        time.Sleep(1 * time.Minute)
        lastBuild := m.getLastBuildTime()
        idle := time.Since(lastBuild)

        if idle > BuildKitDownTimeout {
            m.terminateVM()  // delete completely, recreate when next needed
        } else if idle > BuildKitIdleTimeout {
            m.stopVM()       // stop but keep the disk (SSD cache)
        }
    }
}
```

---

## 9. Storage and Data Transfer

### 9.1 Storage Abstraction Layer

**File**:`sky/data/storage.py`

```python
class Storage:
    """Cloud storage abstraction supporting S3, GCS, Azure, OCI, etc."""

    mode: StorageMode   # MOUNT | COPY | MOUNT_CACHED
    stores: Dict[StoreType, AbstractStore]

class StorageMode(enum.Enum):
    MOUNT = 'MOUNT'                # Read-only FUSE mount
    COPY = 'COPY'                  # One-time rsync
    MOUNT_CACHED = 'MOUNT_CACHED'  # Write-back cache (rclone VFS)
```

### 9.2 FUSE Mount Commands

**File**:`sky/data/mounting_utils.py`

```python
# GCS mount (gcsfuse):
def get_gcs_mount_cmd(bucket_name, mount_path, bucket_sub_path=None):
    return (f'gcsfuse --log-file {log_file} '
            '--debug_fuse_errors '
            '-o allow_other '
            '--implicit-dirs '
            f'--stat-cache-capacity {_STAT_CACHE_CAPACITY} '
            f'--stat-cache-ttl {_STAT_CACHE_TTL} '
            f'--type-cache-ttl {_TYPE_CACHE_TTL} '
            f'--rename-dir-limit {_RENAME_DIR_LIMIT} '
            f'{bucket_sub_path_arg}'
            f'{bucket_name} {mount_path}')

# S3-compatible mount (architecture-aware):
def _get_s3_compatible_mount_cmd(bucket_name, mount_path, ...):
    """Uses goofys (x86_64) or rclone (ARM64)."""
    # ARCH=$(uname -m)
    # if aarch64/arm64 → rclone mount
    # else → goofys (faster for x86)
```

### 9.3 Data Transfer Patterns

| Method | Tool | Purpose | LaunchKit counterpart |
|---|---|---|---|
| Local → Cloud Storage | rsync / cloud CLI | Upload build source | GCS presigned URL |
| Cloud Storage → VM | gcsfuse / goofys | Build context | Source mount on the BuildKit VM |
| VM → Cloud Storage | rclone write-back | Build artifacts | Image push to AR |
| Cloud → Cloud | GCP Storage Transfer | S3 ↔ GCS migration | R2 ↔ GCS (future) |
| VM → VM | SKYPILOT_NODE_IPS | Multi-node training | GPU cluster communication |

**Lesson for LaunchKit**:

```go
// LaunchKit: Build source upload + BuildKit access
//
// Current approach: GCS presigned URL (same region as the BuildKit VM)
// SkyPilot's hint: could switch to mounting directly with gcsfuse, skipping the download step
//
// Phase 1: presigned URL (simple, already implemented)
// Phase 2: gcsfuse mount on the BuildKit VM → zero-copy access to the build context
```

---

## 10. Job Scheduling and Task Management

### 10.1 Job State Machine

**File**:`sky/jobs/state.py`

```python
class ManagedJobScheduleState(enum.Enum):
    PENDING = 'PENDING'              # waiting for resources
    LAUNCHING = 'LAUNCHING'          # creating the cluster
    ALIVE = 'ALIVE'                  # Controller running, task executing
    ALIVE_WAITING = 'ALIVE_WAITING'  # waiting to restart (multi-task)
    DONE = 'DONE'                    # done (terminal)
    FAILED_CONTROLLER = 'FAILED'     # Controller died unexpectedly
```

### 10.2 Scheduler and Parallelism Control

**File**:`sky/jobs/scheduler.py`

```python
def submit_jobs(job_ids, dag_yaml_path, env_file_path, priority, ...):
    """Submit jobs to scheduler.
    1. Store the YAML + env in the DB
    2. Set the state to PENDING
    3. Trigger maybe_start_controllers()
    """
    state.scheduler_set_waiting(job_ids)
    maybe_start_controllers()

def maybe_start_controllers():
    """Spawn controller processes (file-lock guarded).

    Resource limits:
    1. Launch parallelism: number of jobs in STARTING/RECOVERING at the same time (CPU-limited)
    2. Job parallelism: number of jobs in RUNNING at the same time (memory-limited)
    3. Pool parallelism: number of jobs in the pool (worker-limited)
    """
    with file_lock(JOB_CONTROLLER_PID_LOCK):
        alive_count = count_alive_controllers()
        target_count = get_number_of_jobs_controllers()
        for _ in range(target_count - alive_count):
            spawn_controller_process()
```

### 10.3 Job Controller

**File**:`sky/jobs/controller.py`

Every managed job has its own controller process:

```python
class JobController:
    """Controls lifecycle of a single managed job."""

    def __init__(self, job_id, starting, starting_lock, starting_signal,
                 pool=None, rank=None):
        self.job_id = job_id

    async def run(self):
        """Main lifecycle:
        1. Load DAG from DB
        2. Set up environment
        3. Launch via CloudVmRayBackend (or pool)
        4. Monitor execution
        5. Handle preemptions via StrategyExecutor
        6. Persist state transitions
        7. Cleanup on completion
        """

@contextlib.asynccontextmanager
async def scheduled_launch(job_id, starting, starting_lock, starting_signal):
    """Coordinate concurrent launches with async condition variable.
    - Entry: PENDING → LAUNCHING
    - Exit: LAUNCHING → ALIVE
    - Enforces LAUNCHES_PER_WORKER concurrency limit
    """
```

### 10.4 K8s Annotation Tracking

```python
# tag job metadata on the K8s pod (queryable, auditable)
def _add_k8s_annotations(task, job_id):
    annotations = {
        'skypilot-managed-job-id': str(job_id),
        'skypilot-managed-job-name': str(task.name),
    }
```

**Lesson for LaunchKit**: build job scheduling:

```go
// LaunchKit: Build job state machine
type BuildStatus string
const (
    BuildPending   BuildStatus = "pending"    // in the river queue
    BuildBuilding  BuildStatus = "building"   // BuildKit is building
    BuildPushing   BuildStatus = "pushing"    // push the image to AR
    BuildDeploying BuildStatus = "deploying"  // deploy to Cloud Run
    BuildSucceeded BuildStatus = "succeeded"
    BuildFailed    BuildStatus = "failed"
)

// River job queue already provides:
// - retries with backoff
// - parallelism control (MaxWorkers)
// - priorities
// - dead-letter queue
// No need to reinvent — river has it covered
```

---

## 11. Multi-Tenancy and RBAC

### 11.1 User Identity

**File**:`sky/utils/common_utils.py`, `sky/users/rbac.py`

```python
# user identification: MD5 hash of the username (8 hex characters)
user_hash = hashlib.md5(username.encode()).hexdigest()[:8]

# stored in:
# - job_info.user_hash (who submitted the job)
# - clusters.owner (who created the cluster)
# - workspace permissions (who can access)
```

### 11.2 RBAC System

```python
class RoleName(str, enum.Enum):
    ADMIN = 'admin'  # full access
    USER = 'user'    # restricted access (blocklist)

_DEFAULT_USER_BLOCKLIST = [
    {'path': '/workspaces/config', 'method': 'POST'},
    {'path': '/workspaces/update', 'method': 'POST'},
    {'path': '/workspaces/create', 'method': 'POST'},
    {'path': '/users/delete', 'method': 'POST'},
    {'path': '/users/create', 'method': 'POST'},
]

# implemented with the casbin framework (thread-safe, SQLAlchemy adapter)
class PermissionService:
    enforcer: casbin.SyncedEnforcer
    _cache_ttl: int = 3600  # 1 hour cache
```

### 11.3 Workspace Isolation

**File**:`sky/workspaces/core.py`

```python
# logical isolation: multiple workspaces within the same SkyPilot deployment
workspaces:
    workspace1:
        private: true
        allowed_users: ['user1', 'user2']
    workspace2:
        private: false  # public

# filtered automatically on queries
def get_clusters_for_user(user_id):
    accessible = permission_service.get_accessible_workspace_names(user_id)
    return db.query(Cluster).filter(Cluster.workspace.in_(accessible))
```

### 11.4 API Server Middleware Stack

**File**:`sky/server/server.py`

```python
# FastAPI middleware stack:
app.add_middleware(RequestIDMiddleware)           # Request tracing
app.add_middleware(SecurityHeadersMiddleware)     # CSP, CORS
app.add_middleware(RBACMiddleware)                # permission check
app.add_middleware(BearerTokenMiddleware)         # JWT validation
app.add_middleware(BasicAuthMiddleware)           # Basic auth
app.add_middleware(AuthProxyMiddleware)           # External auth proxy
app.add_middleware(InitializeRequestAuthUserMiddleware)  # initialize auth context
```

**Lesson for LaunchKit**: we already have an Auth0 + RBAC design, and SkyPilot's patterns confirm our direction:

```go
// LaunchKit: a similar design already exists (ARCHITECTURE.md)
// - Auth0 as IdP
// - RBAC: owner / admin / deployer / viewer
// - Team-scoped isolation (corresponds to SkyPilot's workspace)
// - Middleware: Auth → RBAC → Rate Limit → Audit
//
// SkyPilot's casbin integration is a good reference — but for Phase 1 simple
// role-based middleware is enough, without the complexity of casbin
```

---

## 12. Kubernetes Integration Patterns

### 12.1 Pod Provisioning

**File**:`sky/provision/kubernetes/instance.py`

```python
def _create_pods(region, cluster_name, config, tags, count, ...):
    """Core pod creation:
    - Namespace isolation: get_namespace_from_config(provider_config)
    - Label injection: skypilot-cluster-name, skypilot-user
    - GPU scheduling: nvidia.com/gpu resource requests
    - HA mode: Kubernetes Deployment (auto-respawn)
    """

# GPU Resource Keys:
SUPPORTED_GPU_RESOURCE_KEYS = {
    'nvidia': 'nvidia.com/gpu',
    'amd':    'amd.com/gpu',
    'google': 'google.com/gpu',
    'tpu':    'cloud.google.com/tpu',
}

# Kueue integration (fair scheduling + priorities):
# - ProvisioningRequest for DWS
# - PodSet merging for multi-node
# - Resource request consistency
```

### 12.2 HA Controller Deployment

```python
def _wait_for_deployment_pod(namespace, deployment_name, ...):
    """HA mode: K8s Deployment instead of bare pods.
    - Auto-respawn on failure
    - PVC at /home/sky for persistent state
    - deployment.spec.replicas for scaling
    """
```

**Lesson for LaunchKit**: if we later need to run builds on K8s:

```yaml
# LaunchKit: K8s BuildKit pod(Phase 2/BYOC scenario)
apiVersion: v1
kind: Pod
metadata:
  labels:
    app: buildkit
    launchkit.dev/team: ${TEAM_ID}
    launchkit.dev/project: ${PROJECT_ID}
  annotations:
    launchkit.dev/build-id: ${BUILD_ID}
spec:
  containers:
  - name: buildkit
    image: moby/buildkit:latest
    resources:
      requests:
        cpu: "4"
        memory: "8Gi"
      limits:
        cpu: "4"
        memory: "8Gi"
    volumeMounts:
    - name: cache
      mountPath: /var/lib/buildkit
  volumes:
  - name: cache
    persistentVolumeClaim:
      claimName: buildkit-cache-pvc  # persistent build cache
```

---

## 13. LaunchKit Use Cases

### Scenario 1: Dynamic Build VM Selection

A user creates a new project → the Plan Engine analyzes the build needs → automatically picks the cheapest VM:

```
Node.js (small)    → e2-small      $0.017/hr  (2 vCPU, 2 GiB)
Python/Django       → e2-medium     $0.034/hr  (2 vCPU, 4 GiB)
Go/Rust (large)    → e2-standard-4 $0.134/hr  (4 vCPU, 16 GiB)
Monorepo (Turbo)   → e2-standard-8 $0.268/hr  (8 vCPU, 32 GiB)
ML model build     → n1-standard-4 + T4 $0.84/hr (GPU-accelerated)
```

Borrowing SkyPilot's catalog + `cpus='4+'` syntax, the Plan Engine can select automatically:

```go
func (p *PlanEngine) selectBuildResources(project *Project) ResourceSpec {
    switch {
    case project.HasGPUDependency():
        return ResourceSpec{Accelerators: "T4:1", CPUs: "4+"}
    case project.IsMonorepo() && project.ServiceCount() > 3:
        return ResourceSpec{CPUs: "8+", Memory: "16+"}
    case project.Language == "rust" || project.Language == "go":
        return ResourceSpec{CPUs: "4+", Memory: "8+"}
    default:
        return ResourceSpec{CPUs: "2+", Memory: "4+"}
    }
}
```

### Scenario 2: Spot Recovery for GPU Training Jobs

A user submits a GPU training job through Claude → LaunchKit uses the SkyPilot SDK:

```python
# LaunchKit GPU Worker calls the SkyPilot Python SDK
import sky

task = sky.Task(
    name=f'launchkit-{project_id}-train',
    setup='pip install -r requirements.txt',
    run='python train.py --checkpoint-dir /checkpoint',
)
task.set_file_mounts({
    '/checkpoint': sky.Storage(name=f'launchkit-{project_id}-ckpt',
                               mode=sky.StorageMode.MOUNT_CACHED),
    '/data': sky.Storage(name=f'launchkit-{project_id}-data',
                          mode=sky.StorageMode.MOUNT),
})
task.set_resources(sky.Resources(
    accelerators='L4:1',
    use_spot=True,                    # Spot instance (saves 60-80%)
    job_recovery={'max_restarts_on_errors': 3},
))

# submit as a managed job → automatic preemption recovery
sky.jobs.launch(task)
```

### Scenario 3: BuildKit VM Pool Autoscaling

At peak hours (e.g., Monday morning) the build queue depth rises → scale out the BuildKit VMs dynamically:

```
09:00  queue=0   VMs=1  (always-on primary)
09:15  queue=5   VMs=1  upscale_counter=1/2
09:16  queue=8   VMs=1  upscale_counter=2/2 → SCALE UP
09:17  queue=8   VMs=3  (2 new VMs provisioning)
09:30  queue=0   VMs=3  downscale_counter=1/10
09:40  queue=0   VMs=3  downscale_counter=10/10 → SCALE DOWN
09:41  queue=0   VMs=1  (back to the always-on primary)
```

Borrowing SkyPilot's `_AutoscalerWithHysteresis`:
- Scale-up: 1 minute delay (builds are latency-sensitive)
- Scale-down: 10 minute delay (avoids repeated cold starts)
- New VMs use spot/preemptible (saves 70%, failed builds can be retried)

### Scenario 4: Multi-Region Failover for Deployment

Borrowing SkyPilot's `RetryingVmProvisioner` + `blocked_resources`:

```go
func (o *Orchestrator) DeployToCloudRun(ctx context.Context, spec DeploySpec) error {
    blocked := make(map[string]bool)
    regions := []string{"us-central1", "us-east1", "us-west1", "europe-west1"}

    for _, region := range regions {
        if blocked[region] {
            continue
        }

        err := cloudrun.Deploy(ctx, spec, region)
        if err == nil {
            return nil
        }

        if isCapacityError(err) || isQuotaError(err) {
            blocked[region] = true
            log.Warn("region blocked, trying next", "region", region, "err", err)
            continue
        }
        return err  // unrecoverable
    }
    return ErrAllRegionsExhausted
}
```

### Scenario 5: Cost Control with Budget Enforcement

Borrowing SkyPilot's `max_hourly_cost` + autostop:

```go
// LaunchKit: budget enforcement (already designed in ARCHITECTURE.md)
// SkyPilot's patterns confirm our direction:
//
// 1. max_hourly_cost → filter out over-budget instances at catalog query time
// 2. autostop → idle resources are reclaimed automatically
// 3. budget alerts → notify when approaching the limit
// 4. scale_to_zero → scale down automatically when the budget is exceeded

type BudgetEnforcer struct {
    MonthlyLimitUSD float64
    Action          string  // "alert_only" | "scale_to_zero"
}

func (b *BudgetEnforcer) Check(currentSpend float64) {
    ratio := currentSpend / b.MonthlyLimitUSD
    if ratio >= 0.8 {
        notify.Alert(fmt.Sprintf("Budget %.0f%% used", ratio*100))
    }
    if ratio >= 1.0 && b.Action == "scale_to_zero" {
        orchestrator.ScaleToZero()  // stop all non-essential resources
    }
}
```

### Scenario 6: Resource Orphan Reconciliation

Borrowing SkyPilot's `global_user_state` + periodic scanning:

```go
// LaunchKit: orphan reconciliation is already designed in ARCHITECTURE.md
// SkyPilot confirms this pattern is necessary:
//
// periodic river job → list all cloud resources → compare with the DB → clean up orphans
//
// SkyPilot's global_user_state.py tracks all clusters in SQLite,
// and we do the same with the resources table in Neon Postgres.
// The key point: every provision writes to the DB, and so does every teardown,
// then a periodic reconcile handles inconsistencies (crash, timeout, manual deletion).
```

---

## 14. Industry Case Studies and Cost Data

### 14.1 Cost Optimization Data

| Company | Scenario | Cost savings | Other results |
|---|---|---|---|
| **Avataar** | Multi-cloud GenAI | **11x cost reduction** | Unlocked GPU capacity on neoclouds |
| **IP Copilot** | Patent AI / LLM eval | **80% spend reduction** | 3x faster evaluation |
| **Jam & Tea Studios** | GenAI for games | **80% cheaper** | Moved off hosted APIs |
| **Sinay** | Batch video inference | **62% cheaper** | 6x faster setup |
| **Salk Institute** | Neuroscience (CPU spot) | **5x lower cost** | 3x faster than the on-prem cluster |
| **Shopify** | All AI training | Minutes vs days | Unified multi-cloud interface |
| **Abridge** | Multi-cloud AI infra | — | 10x faster development cycle |

### 14.2 Shopify Case (January 2026)

Shopify uses SkyPilot in production to manage:
- **Nebius** H200s with InfiniBand (large-scale training)
- **GCP** L4s (development) + CPU (data processing)

Key patterns:
- Custom policy plugin: intercepts every request for label validation, provider routing, and config injection
- Mandatory cost attribution labels: `showback_cost_owner_ref`
- Kueue integration: fair-share scheduling with priority classes
  - emergency > interactive > automated > lowest
- 200TB-2PB Nebius storage, 80 GiB/s read bandwidth
- Automatic caching of Python packages and model weights

### 14.3 Spot Instance Cost Comparison

| Instance Type | On-Demand | Spot/Preemptible | Savings |
|---|---|---|---|
| GCP e2-standard-4 | $0.134/hr | $0.040/hr | **70%** |
| GCP n1-standard-4 + T4 | $0.95/hr | $0.35/hr | **63%** |
| AWS p3.2xlarge (V100) | $3.06/hr | $0.92/hr | **70%** |
| GCP a2-highgpu-1g (A100) | $3.67/hr | $1.10/hr | **70%** |

### 14.4 SkyServe Autoscaling Benefits

- LMSys Chatbot Arena: 800K+ requests, ~10 open LLMs
- Cross-region serving: ~50% cost savings
- Spot replicas: an extra 3x savings
- Network latency ~300ms vs compute time of seconds → cross-region is feasible

---

## 15. Academic Paper Index

| Paper | Venue | Topic | Lesson for LaunchKit |
|---|---|---|---|
| [SkyPilot: An Intercloud Broker for Sky Computing](https://www.usenix.org/system/files/nsdi23-yang-zongheng.pdf) | NSDI 2023 | ILP optimizer, cross-cloud provisioning | Price optimization algorithm |
| [Can't Be Late: Optimizing Spot Instance Savings under Deadlines](https://www.usenix.org/conference/nsdi24/presentation/wu-zhanghao) | NSDI 2024 | Spot policy, time-sliced greedy | GPU training spot strategy |
| [SkyServe: AI Serving Across Regions and Clouds](https://arxiv.org/pdf/2411.01438) | EuroSys 2025 | Multi-region serving, autoscaling | Cloud Run autoscaling patterns |
| [Sky Computing (Vision)](https://arxiv.org/abs/2205.07147) | HotOS 2021 / arXiv | Sky computing concept | Long-term architecture vision |

**Spot traces dataset**: [github.com/skypilot-org/spot-traces](https://github.com/skypilot-org/spot-traces)

---

## Appendix: SkyPilot Key File Index

| System | File path | Size | Purpose |
|---|---|---|---|
| Cloud abstraction | `sky/clouds/cloud.py` | 42KB | Base Cloud class |
| GCP implementation | `sky/clouds/gcp.py` | 1,565 lines | GCP provider |
| Optimizer | `sky/optimizer.py` | 74KB | ILP/DP optimizer |
| Resource | `sky/resources.py` | 79KB | Resource class |
| Provisioner Router | `sky/provision/__init__.py` | 11KB | Cloud routing |
| Provisioner Core | `sky/provision/provisioner.py` | 35KB | Provisioning orchestration |
| GCP Provisioner | `sky/provision/gcp/instance.py` | 671 lines | GCE lifecycle |
| GCP Instance Utils | `sky/provision/gcp/instance_utils.py` | 1,989 lines | API calls |
| K8s Provisioner | `sky/provision/kubernetes/instance.py` | 2,270 lines | Pod provisioning |
| Backend | `sky/backends/cloud_vm_ray_backend.py` | 306KB | RetryingVmProvisioner |
| Backend Utils | `sky/backends/backend_utils.py` | 193KB | Config generation |
| Catalog Common | `sky/catalog/common.py` | — | Price queries |
| GCP Catalog | `sky/catalog/gcp_catalog.py` | 705 lines | GCP instance types |
| Storage | `sky/data/storage.py` | 232KB | Cloud storage |
| Mounting | `sky/data/mounting_utils.py` | — | FUSE mount commands |
| Autoscaler | `sky/serve/autoscalers.py` | 1,400+ lines | Autoscaling |
| Replica Manager | `sky/serve/replica_managers.py` | 1,200+ lines | Replica lifecycle |
| Load Balancing | `sky/serve/load_balancing_policies.py` | 263 lines | LB policies |
| Job State | `sky/jobs/state.py` | 2,800+ lines | Job DB |
| Job Scheduler | `sky/jobs/scheduler.py` | 18.7KB | Job scheduling |
| Job Controller | `sky/jobs/controller.py` | 108KB | Job lifecycle |
| Recovery | `sky/jobs/recovery_strategy.py` | 1,200+ lines | Spot recovery |
| Autostop | `sky/skylet/autostop_lib.py` | — | Idle detection |
| RBAC | `sky/users/rbac.py` | — | Role-based access |
| Workspaces | `sky/workspaces/core.py` | 25KB | Workspace isolation |
| API Server | `sky/server/server.py` | 140KB | FastAPI app |
| Global State | `sky/global_user_state.py` | — | SQLite cluster DB |
