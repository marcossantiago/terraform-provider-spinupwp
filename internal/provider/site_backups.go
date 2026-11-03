package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/marcossantiago/terraform-provider-spinupwp/internal/client"
)

// siteBackupsModel is the backups block of spinupwp_site.
type siteBackupsModel struct {
	StorageProviderID types.Int64         `tfsdk:"storage_provider_id"`
	Bucket            types.String        `tfsdk:"bucket"`
	Region            types.String        `tfsdk:"region"`
	StorageClass      types.String        `tfsdk:"storage_class"`
	IncludeDatabase   types.Bool          `tfsdk:"include_database"`
	PathsToExclude    types.List          `tfsdk:"paths_to_exclude"`
	Daily             *dailyBackupsModel  `tfsdk:"daily"`
	Weekly            *weeklyBackupsModel `tfsdk:"weekly"`
	Monthly           *monthlyBackupModel `tfsdk:"monthly"`
}

type backupContentModel struct {
	Files         types.Bool  `tfsdk:"files"`
	Database      types.Bool  `tfsdk:"database"`
	RetentionDays types.Int64 `tfsdk:"retention_days"`
}

type dailyBackupsModel struct {
	Hours []types.Int64 `tfsdk:"hours"`
	backupContentModel
}

type weeklyBackupsModel struct {
	Days []types.Int64 `tfsdk:"days"`
	Hour types.Int64   `tfsdk:"hour"`
	backupContentModel
}

type monthlyBackupModel struct {
	Days []monthDayModel `tfsdk:"days"`
	Hour types.Int64     `tfsdk:"hour"`
	backupContentModel
}

type monthDayModel struct {
	Week types.String `tfsdk:"week"`
	Day  types.Int64  `tfsdk:"day"`
}

var (
	hourValidator    = int64validator.Between(0, 23)
	weekdayValidator = int64validator.Between(0, 6)
)

func backupsAttribute() schema.Attribute {
	return schema.SingleNestedAttribute{
		Description: "Scheduled backups. Needs at least one of `daily`, `weekly` or `monthly`. " +
			"Removing the block turns every schedule off; SpinupWP keeps the storage settings and existing backups.",
		Optional: true,
		Attributes: map[string]schema.Attribute{
			"storage_provider_id": schema.Int64Attribute{
				Description: "ID of a storage provider configured in SpinupWP (Account Settings → Storage Providers).",
				Required:    true,
				Validators:  []validator.Int64{int64validator.AtLeast(1)},
			},
			"bucket": schema.StringAttribute{
				Description: "Bucket to store backups in (a path for SFTP).",
				Required:    true,
			},
			"region": schema.StringAttribute{
				Description:   "Bucket region. Required for every storage service except SFTP, Backblaze and Cloudflare.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"storage_class": schema.StringAttribute{
				Description: "Storage class. Required for AWS S3: `STANDARD`, `STANDARD_IA`, `ONEZONE_IA`, `INTELLIGENT_TIERING` or `GLACIER_IR`.",
				Optional:    true,
				Validators: []validator.String{stringvalidator.OneOf(
					"STANDARD", "STANDARD_IA", "ONEZONE_IA", "INTELLIGENT_TIERING", "GLACIER_IR")},
			},
			"include_database": schema.BoolAttribute{
				Description:   "Back up the site's database (in schedules with `database = true`). Defaults to the site's current setting.",
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"paths_to_exclude": schema.ListAttribute{
				Description:   "Paths to leave out of file backups. Defaults to SpinupWP's list.",
				ElementType:   types.StringType,
				Optional:      true,
				Computed:      true,
				PlanModifiers: []planmodifier.List{listplanmodifier.UseStateForUnknown()},
			},
			"daily": schema.SingleNestedAttribute{
				Description: "Daily backups. Unset disables them.",
				Optional:    true,
				Attributes: contentAttributes(map[string]schema.Attribute{
					"hours": schema.SetAttribute{
						Description: "Hours (0-23, server time) to run at. How many you can set depends on your SpinupWP plan.",
						ElementType: types.Int64Type,
						Required:    true,
						Validators:  []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueInt64sAre(hourValidator)},
					},
				}),
			},
			"weekly": schema.SingleNestedAttribute{
				Description: "Weekly backups. Unset disables them.",
				Optional:    true,
				Attributes: contentAttributes(map[string]schema.Attribute{
					"days": schema.SetAttribute{
						Description: "Weekdays to run on, 0 (Sunday) to 6 (Saturday).",
						ElementType: types.Int64Type,
						Required:    true,
						Validators:  []validator.Set{setvalidator.SizeAtLeast(1), setvalidator.ValueInt64sAre(weekdayValidator)},
					},
					"hour": schema.Int64Attribute{
						Description: "Hour (0-23, server time) to run at.",
						Required:    true,
						Validators:  []validator.Int64{hourValidator},
					},
				}),
			},
			"monthly": schema.SingleNestedAttribute{
				Description: "Monthly backups. Unset disables them.",
				Optional:    true,
				Attributes: contentAttributes(map[string]schema.Attribute{
					"days": schema.SetNestedAttribute{
						Description: "Days of the month to run on, e.g. `{ week = \"first\", day = 1 }` for the first Monday.",
						Required:    true,
						Validators:  []validator.Set{setvalidator.SizeAtLeast(1)},
						NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
							"week": schema.StringAttribute{
								Description: "`first`, `second`, `third` or `last`.",
								Required:    true,
								Validators:  []validator.String{stringvalidator.OneOf("first", "second", "third", "last")},
							},
							"day": schema.Int64Attribute{
								Description: "Weekday, 0 (Sunday) to 6 (Saturday).",
								Required:    true,
								Validators:  []validator.Int64{weekdayValidator},
							},
						}},
					},
					"hour": schema.Int64Attribute{
						Description: "Hour (0-23, server time) to run at.",
						Required:    true,
						Validators:  []validator.Int64{hourValidator},
					},
				}),
			},
		},
	}
}

// contentAttributes are shared by the daily, weekly and monthly schedules.
func contentAttributes(extra map[string]schema.Attribute) map[string]schema.Attribute {
	attrs := map[string]schema.Attribute{
		"files": schema.BoolAttribute{
			Description: "Back up the site's files. Defaults to `true`.",
			Optional:    true, Computed: true, Default: booldefault.StaticBool(true),
		},
		"database": schema.BoolAttribute{
			Description: "Back up the database. Needs `include_database`. Defaults to `true`.",
			Optional:    true, Computed: true, Default: booldefault.StaticBool(true),
		},
		"retention_days": schema.Int64Attribute{
			Description: "Days to keep these backups. Unset keeps them forever.",
			Optional:    true,
			Validators:  []validator.Int64{int64validator.AtLeast(1)},
		},
	}
	for k, v := range extra {
		attrs[k] = v
	}
	return attrs
}

// syncBackups makes SpinupWP match the planned backups block. site is the current site.
func (r *siteResource) syncBackups(ctx context.Context, site *client.Site, plan *siteBackupsModel, changed bool) error {
	if plan == nil {
		if !hasSchedule(site.Backups.Schedule) {
			return nil
		}
		_, err := r.client.UpdateBackupSchedule(ctx, site.ID, client.BackupScheduleRequest{}) // all off
		return err
	}
	if !changed {
		return nil
	}

	settings := client.BackupSettingsRequest{
		StorageProviderID: plan.StorageProviderID.ValueInt64(),
		Bucket:            plan.Bucket.ValueString(),
		Region:            plan.Region.ValueString(),
		StorageClass:      plan.StorageClass.ValueString(),
		PathsToExclude:    site.Backups.PathsToExclude,
	}
	if known(plan.PathsToExclude) {
		var paths []string
		if d := plan.PathsToExclude.ElementsAs(ctx, &paths, false); d.HasError() {
			return fmt.Errorf("reading paths_to_exclude: %v", d)
		}
		settings.PathsToExclude = strings.Join(paths, "\n")
	}
	includeDatabase := site.Backups.Database
	if known(plan.IncludeDatabase) {
		includeDatabase = plan.IncludeDatabase.ValueBool()
	}
	if includeDatabase {
		if site.Database == nil {
			return fmt.Errorf("include_database is true but the site has no database")
		}
		settings.DatabaseID = &site.Database.ID
	}
	if _, err := r.client.UpdateBackupSettings(ctx, site.ID, settings); err != nil {
		return fmt.Errorf("updating backup settings: %w", err)
	}
	if _, err := r.client.UpdateBackupSchedule(ctx, site.ID, scheduleRequest(plan)); err != nil {
		return fmt.Errorf("updating backup schedule: %w", err)
	}
	return nil
}

func hasSchedule(s client.BackupSchedule) bool {
	return len(s.Daily.TimeOfDay) > 0 || len(s.Weekly.DayOfWeek) > 0 || len(s.Monthly.DayOfMonth) > 0
}

func scheduleRequest(m *siteBackupsModel) client.BackupScheduleRequest {
	var r client.BackupScheduleRequest
	if d := m.Daily; d != nil {
		r.Daily = &client.DailyBackups{BackupContent: content(d.backupContentModel), TimeOfDay: int64s(d.Hours)}
	}
	if w := m.Weekly; w != nil {
		r.Weekly = &client.WeeklyBackups{BackupContent: content(w.backupContentModel), DayOfWeek: int64s(w.Days), TimeOfDay: w.Hour.ValueInt64Pointer()}
	}
	if mo := m.Monthly; mo != nil {
		days := make([]client.MonthDay, len(mo.Days))
		for i, d := range mo.Days {
			days[i] = client.MonthDay{WeekOfMonth: d.Week.ValueString(), DayOfWeek: d.Day.ValueInt64()}
		}
		r.Monthly = &client.MonthlyBackups{BackupContent: content(mo.backupContentModel), DayOfMonth: days, TimeOfDay: mo.Hour.ValueInt64Pointer()}
	}
	return r
}

func content(c backupContentModel) client.BackupContent {
	return client.BackupContent{
		BackupFiles:     c.Files.ValueBool(),
		BackupDatabase:  c.Database.ValueBool(),
		RetentionPeriod: c.RetentionDays.ValueInt64Pointer(),
	}
}

// backupsFromSite returns the block as SpinupWP reports it: nil when no schedule runs.
// storage_class is never returned, so it is kept from prior state.
func backupsFromSite(ctx context.Context, s *client.Site, prior *siteBackupsModel, diags *diag.Diagnostics) *siteBackupsModel {
	b := s.Backups
	if !hasSchedule(b.Schedule) {
		return nil
	}
	m := &siteBackupsModel{
		IncludeDatabase: types.BoolValue(b.Database),
		StorageClass:    types.StringNull(),
	}
	if prior != nil {
		m.StorageClass = prior.StorageClass
	}
	if b.StorageProvider != nil {
		m.StorageProviderID = types.Int64Value(b.StorageProvider.ID)
		m.Bucket = types.StringValue(b.StorageProvider.Bucket)
		m.Region = types.StringValue(b.StorageProvider.Region)
	}

	paths := []attr.Value{}
	for _, p := range strings.Split(b.PathsToExclude, "\n") {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, types.StringValue(p))
		}
	}
	list, d := types.ListValue(types.StringType, paths)
	diags.Append(d...)
	m.PathsToExclude = list

	if d := b.Schedule.Daily; len(d.TimeOfDay) > 0 {
		m.Daily = &dailyBackupsModel{Hours: int64Values(d.TimeOfDay), backupContentModel: contentModel(d.BackupContent)}
	}
	if w := b.Schedule.Weekly; len(w.DayOfWeek) > 0 {
		m.Weekly = &weeklyBackupsModel{Days: int64Values(w.DayOfWeek), Hour: types.Int64PointerValue(w.TimeOfDay), backupContentModel: contentModel(w.BackupContent)}
	}
	if mo := b.Schedule.Monthly; len(mo.DayOfMonth) > 0 {
		days := make([]monthDayModel, len(mo.DayOfMonth))
		for i, d := range mo.DayOfMonth {
			days[i] = monthDayModel{Week: types.StringValue(d.WeekOfMonth), Day: types.Int64Value(d.DayOfWeek)}
		}
		m.Monthly = &monthlyBackupModel{Days: days, Hour: types.Int64PointerValue(mo.TimeOfDay), backupContentModel: contentModel(mo.BackupContent)}
	}
	return m
}

func contentModel(c client.BackupContent) backupContentModel {
	return backupContentModel{
		Files:         types.BoolValue(c.BackupFiles),
		Database:      types.BoolValue(c.BackupDatabase),
		RetentionDays: types.Int64PointerValue(c.RetentionPeriod),
	}
}

func int64s(vs []types.Int64) []int64 {
	out := make([]int64, len(vs))
	for i, v := range vs {
		out[i] = v.ValueInt64()
	}
	return out
}

func int64Values(vs []int64) []types.Int64 {
	out := make([]types.Int64, len(vs))
	for i, v := range vs {
		out[i] = types.Int64Value(v)
	}
	return out
}
