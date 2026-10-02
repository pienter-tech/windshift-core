<script>
  import Modal from '../../dialogs/Modal.svelte';
  import ModalHeader from '../../dialogs/ModalHeader.svelte';
  import DialogFooter from '../../dialogs/DialogFooter.svelte';
  import Button from '../../components/Button.svelte';
  import Input from '../../components/Input.svelte';
  import Select from '../../components/Select.svelte';
  import Toggle from '../../components/Toggle.svelte';
  import Textarea from '../../components/Textarea.svelte';
  import { Plus, Trash2 } from '@lucide/svelte';
  import { t } from '../../stores/i18n.svelte.js';
  import { listIanaTimezones } from '../../utils/timeUtils.js';
  import { api } from '../../api.js';

  let {
    isOpen = $bindable(false),
    calendar = null,
    workspaceId = null,
    onSave,
    onClose,
  } = $props();

  const weekdays = ['monday', 'tuesday', 'wednesday', 'thursday', 'friday', 'saturday', 'sunday'];

  const baseTimezoneIds = listIanaTimezones();

  function blankIntervals() {
    return Object.fromEntries(weekdays.map((day) => [day, []]));
  }

  function blankForm() {
    return {
      name: '',
      description: '',
      timezone: 'UTC',
      is_default: false,
      apply_to_ongoing: false,
      weekly_intervals: blankIntervals(),
      holidays: [],
    };
  }

  let formData = $state(blankForm());
  let saving = $state(false);
  let error = $state(null);
  let coverage = $state(null);
  let coverageLoading = $state(false);

  const timezoneOptions = $derived.by(() => {
    const zones = formData.timezone && !baseTimezoneIds.includes(formData.timezone)
      ? [formData.timezone, ...baseTimezoneIds]
      : baseTimezoneIds;
    return zones;
  });

  $effect(() => {
    if (!isOpen) return;
    error = null;
    formData = normalize(calendar);
  });

  // Config-time coverage preview: only workspace-owned calendars have a
  // workspace-relative reference to compare against.
  $effect(() => {
    const id = calendar?.id;
    const ownedByWorkspace = !!calendar?.workspace_id;
    if (!isOpen || !id || !workspaceId || !ownedByWorkspace || !api.sla?.getCoveragePreview) {
      coverage = null;
      return;
    }
    let active = true;
    coverageLoading = true;
    api.sla
      .getCoveragePreview(workspaceId, id)
      .then((value) => {
        if (active) coverage = value;
      })
      .catch(() => {
        if (active) coverage = null;
      })
      .finally(() => {
        if (active) coverageLoading = false;
      });
    return () => {
      active = false;
    };
  });

  function formatWeekly(ms) {
    if (ms == null || ms === 0) return '0m';
    const hours = Math.round(ms / 3600000);
    return `${hours}h`;
  }

  function normalize(source) {
    const base = blankForm();
    if (!source) return base;
    const weekly = blankIntervals();
    for (const day of weekdays) {
      const intervals = source.weekly_intervals?.[day];
      if (Array.isArray(intervals)) {
        weekly[day] = intervals.map((interval) => ({
          start: interval.start || '09:00',
          end: interval.end || '17:00',
        }));
      }
    }
    return {
      name: source.name || '',
      description: source.description || '',
      timezone: source.timezone || 'UTC',
      is_default: !!source.is_default,
      apply_to_ongoing: false,
      weekly_intervals: weekly,
      holidays: Array.isArray(source.holidays)
        ? source.holidays.map((holiday) => ({
            date: holiday.date || '',
            month_day: holiday.month_day || '',
            recurring: !!holiday.recurring,
            name: holiday.name || '',
          }))
        : [],
    };
  }

  function addInterval(day) {
    formData.weekly_intervals[day] = [
      ...formData.weekly_intervals[day],
      { start: '09:00', end: '17:00' },
    ];
  }

  function removeInterval(day, index) {
    formData.weekly_intervals[day] = formData.weekly_intervals[day].filter(
      (_, i) => i !== index
    );
  }

  function addHoliday() {
    formData.holidays = [...formData.holidays, { date: '', month_day: '', recurring: false, name: '' }];
  }

  function removeHoliday(index) {
    formData.holidays = formData.holidays.filter((_, i) => i !== index);
  }

  function buildPayload() {
    const weekly = {};
    for (const day of weekdays) {
      const intervals = formData.weekly_intervals[day].filter(
        (interval) => interval.start && interval.end && interval.end > interval.start
      );
      if (intervals.length > 0) weekly[day] = intervals;
    }
    return {
      name: formData.name.trim(),
      description: formData.description,
      timezone: formData.timezone,
      is_default: formData.is_default,
      apply_to_ongoing: formData.apply_to_ongoing,
      weekly_intervals: weekly,
      holidays: formData.holidays
        .filter((holiday) => holiday.recurring ? holiday.month_day : holiday.date)
        .map((holiday) =>
          holiday.recurring
            ? { month_day: holiday.month_day, recurring: true, name: holiday.name }
            : { date: holiday.date, name: holiday.name }
        ),
    };
  }

  async function submit() {
    saving = true;
    error = null;
    try {
      await onSave(buildPayload());
      isOpen = false;
    } catch (err) {
      error = err?.message || t('workspaceSettings.serviceLevels.saveFailed');
    } finally {
      saving = false;
    }
  }
</script>

<Modal
  {isOpen}
  onclose={onClose}
  maxWidth="max-w-3xl"
  onSubmit={submit}
  submitDisabled={saving}
  dataTestid="sla-calendar-dialog"
>
  {#snippet children()}
    <ModalHeader
      title={calendar
        ? t('workspaceSettings.serviceLevels.editCalendar')
        : t('workspaceSettings.serviceLevels.createCalendar')}
      showCloseButton={false}
    />

    <div class="px-6 py-4 max-h-[70vh] overflow-y-auto">
      {#if error}
        <div class="mb-3 text-sm" style="color: var(--ds-text-danger)" data-testid="sla-calendar-error">
          {error}
        </div>
      {/if}

      <div class="form-group">
        <label for="sla-calendar-name">{t('common.name')}</label>
        <Input
          id="sla-calendar-name"
          type="text"
          bind:value={formData.name}
          placeholder={t('workspaceSettings.serviceLevels.namePlaceholder')}
          dataTestid="sla-calendar-name"
        />
      </div>

      <div class="form-group">
        <label for="sla-calendar-description">{t('common.description')}</label>
        <Textarea
          id="sla-calendar-description"
          bind:value={formData.description}
          rows={2}
          placeholder={t('placeholders.optionalDescription')}
          dataTestid="sla-calendar-description"
        />
      </div>

      <div class="form-group">
        <label for="sla-calendar-timezone">{t('workspaceSettings.serviceLevels.timezone')}</label>
        <Select id="sla-calendar-timezone" bind:value={formData.timezone} options={timezoneOptions} />
      </div>

      <fieldset class="form-group">
        <legend>{t('workspaceSettings.serviceLevels.weeklyHours')}</legend>
        {#each weekdays as day (day)}
          <div class="flex items-start gap-2 py-1" data-testid="sla-calendar-day-{day}">
            <span class="w-24 pt-2 text-sm" style="color: var(--ds-text-subtle)">
              {t(`workspaceSettings.serviceLevels.days.${day}`)}
            </span>
            <div class="flex-1">
              {#each formData.weekly_intervals[day] as interval, index}
                <div class="flex items-center gap-2 py-0.5">
                  <Input
                    type="time"
                    bind:value={interval.start}
                    ariaLabel={t('workspaceSettings.serviceLevels.startTime')}
                    dataTestid="sla-calendar-{day}-start-{index}"
                  />
                  <span>–</span>
                  <Input
                    type="time"
                    bind:value={interval.end}
                    ariaLabel={t('workspaceSettings.serviceLevels.endTime')}
                    dataTestid="sla-calendar-{day}-end-{index}"
                  />
                  <Button
                    variant="default"
                    size="small"
                    icon={Trash2}
                    onclick={() => removeInterval(day, index)}
                    title={t('common.remove')}
                    dataTestid="sla-calendar-{day}-remove-{index}"
                  />
                </div>
              {/each}
              <Button
                variant="default"
                size="small"
                icon={Plus}
                onclick={() => addInterval(day)}
                dataTestid="sla-calendar-{day}-add"
              >
                {t('workspaceSettings.serviceLevels.addInterval')}
              </Button>
            </div>
          </div>
        {/each}
      </fieldset>

      <fieldset class="form-group">
        <legend>{t('workspaceSettings.serviceLevels.holidays')}</legend>
        {#each formData.holidays as holiday, index}
          <div class="flex items-center gap-2 py-1">
            {#if holiday.recurring}
              <Input
                type="text"
                bind:value={holiday.month_day}
                placeholder="MM-DD"
                ariaLabel={t('workspaceSettings.serviceLevels.monthDay')}
                dataTestid="sla-calendar-holiday-monthday-{index}"
              />
            {:else}
              <Input
                type="date"
                bind:value={holiday.date}
                ariaLabel={t('workspaceSettings.serviceLevels.date')}
                dataTestid="sla-calendar-holiday-date-{index}"
              />
            {/if}
            <Input
              type="text"
              bind:value={holiday.name}
              placeholder={t('workspaceSettings.serviceLevels.holidayName')}
              ariaLabel={t('workspaceSettings.serviceLevels.holidayName')}
              dataTestid="sla-calendar-holiday-name-{index}"
            />
            <Toggle
              bind:checked={holiday.recurring}
              label={t('workspaceSettings.serviceLevels.recurring')}
              dataTestid="sla-calendar-holiday-recurring-{index}"
            />
            <Button
              variant="default"
              size="small"
              icon={Trash2}
              onclick={() => removeHoliday(index)}
              title={t('common.remove')}
              dataTestid="sla-calendar-holiday-remove-{index}"
            />
          </div>
        {/each}
        <Button variant="default" size="small" icon={Plus} onclick={addHoliday} dataTestid="sla-calendar-holiday-add">
          {t('workspaceSettings.serviceLevels.addHoliday')}
        </Button>
      </fieldset>

      <div class="form-group flex items-center gap-2">
        <Toggle bind:checked={formData.is_default} dataTestid="sla-calendar-default" />
        <span>{t('workspaceSettings.serviceLevels.setDefault')}</span>
      </div>

      {#if calendar}
        <div class="form-group flex items-center gap-2">
          <Toggle bind:checked={formData.apply_to_ongoing} dataTestid="sla-calendar-apply-ongoing" />
          <span>{t('workspaceSettings.serviceLevels.applyToOngoing')}</span>
        </div>
      {/if}

      {#if coverage && coverage.reference === 'team_service_hours'}
        <div
          class="mb-3 rounded border p-3 text-xs"
          style="border-color: var(--ds-border)"
          data-testid="sla-calendar-coverage"
        >
          <div class="font-medium mb-1">{t('workspaceSettings.serviceLevels.coveragePreview')}</div>
          <div style="color: var(--ds-text-subtle)">
            {t('workspaceSettings.serviceLevels.coverageWeekly', {
              sla: formatWeekly(coverage.sla_weekly_ms),
              team: formatWeekly(coverage.team_weekly_ms),
              overlap: formatWeekly(coverage.overlap_weekly_ms),
            })}
          </div>
          {#if coverage.discrepancy && coverage.discrepancy !== 'aligned'}
            <div style="color: var(--ds-text-subtle)">
              {t(`workspaceSettings.serviceLevels.coverageDiscrepancies.${coverage.discrepancy}`)}
            </div>
          {/if}
        </div>
      {/if}
    </div>

    <DialogFooter
      onCancel={onClose}
      onConfirm={submit}
      confirmLabel={calendar ? t('common.save') : t('common.create')}
      loading={saving}
      showKeyboardHint
      confirmTestid="sla-calendar-submit"
    />
  {/snippet}
</Modal>
