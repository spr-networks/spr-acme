import React, { useCallback, useEffect, useMemo, useState } from 'react'
import {
  api,
  useAlert,
  Page,
  ListHeader,
  Card,
  SectionHeader,
  StatTile,
  StatusDot,
  Toggle,
  TextField,
  Loading,
  EmptyState,
  ModalConfirm,
  Badge,
  BadgeText,
  Box,
  Button,
  ButtonText,
  HStack,
  VStack,
  Text,
  Textarea,
  TextareaInput
} from '@spr-networks/plugin-ui'
import { emptyProfileForm, formFromProfile, profileFromForm } from './profileForm'

const PLUGIN_BASE = `/plugins/${api.pluginURI() || 'spr-acme'}`
const COMMON_PROVIDERS = [
  ['cloudflare', 'Cloudflare'],
  ['route53', 'Route 53'],
  ['digitalocean', 'DigitalOcean'],
  ['porkbun', 'Porkbun'],
  ['hetzner', 'Hetzner'],
  ['desec', 'deSEC'],
  ['duckdns', 'DuckDNS'],
  ['technitium', 'Technitium'],
  ['dnsupdate', 'RFC 2136'],
  ['acmedns', 'ACME-DNS']
]

const Label = ({ children }) => (
  <Text
    size="2xs"
    color="$muted500"
    fontWeight="$semibold"
    sx={{ '@base': { letterSpacing: 0.7, textTransform: 'uppercase' } }}
  >
    {children}
  </Text>
)

const Mono = ({ children, ...props }) => (
  <Text
    color="$textLight900"
    sx={{ '@base': { fontFamily: 'monospace' }, _dark: { color: '$textDark50' } }}
    {...props}
  >
    {children}
  </Text>
)

const Segment = ({ options, value, onChange }) => (
  <HStack flexWrap="wrap" gap="$2">
    {options.map((option) => (
      <Button
        key={option.value}
        size="xs"
        borderRadius="$full"
        variant={value === option.value ? 'solid' : 'outline'}
        action={value === option.value ? 'primary' : 'secondary'}
        onPress={() => onChange(option.value)}
      >
        <ButtonText>{option.label}</ButtonText>
      </Button>
    ))}
  </HStack>
)

const InfoRow = ({ label, value, mono = false }) => (
  <HStack justifyContent="space-between" alignItems="flex-start" space="md">
    <Text size="sm" color="$muted500">{label}</Text>
    {mono ? <Mono size="sm" textAlign="right">{value || '—'}</Mono> : <Text size="sm" textAlign="right">{value || '—'}</Text>}
  </HStack>
)

const certificateState = (certificate) => {
  if (certificate.Job?.Running) return { label: certificate.Job.Phase || 'Issuing', action: 'warning' }
  if (certificate.Job?.LastError) return { label: 'Needs attention', action: 'error' }
  if (!certificate.Issued) return { label: 'Not issued', action: 'muted' }
  if (certificate.DaysRemaining <= 14) return { label: `${certificate.DaysRemaining} days left`, action: 'warning' }
  return { label: `${certificate.DaysRemaining} days left`, action: 'success' }
}

const formatDate = (value) => {
  if (!value) return '—'
  try {
    return new Date(value).toLocaleString()
  } catch (_) {
    return value
  }
}

export default function Plugin() {
  const alert = useAlert()
  const [tab, setTab] = useState('overview')
  const [overview, setOverview] = useState(null)
  const [providerCodes, setProviderCodes] = useState([])
  const [providerInfo, setProviderInfo] = useState(null)
  const [accountForm, setAccountForm] = useState({ Email: '', Provider: '', CA: 'staging', AcceptTOS: false })
  const [credentialValues, setCredentialValues] = useState({})
  const [clearCredentials, setClearCredentials] = useState([])
  const [profileForm, setProfileForm] = useState(emptyProfileForm())
  const [editingID, setEditingID] = useState(null)
  const [busy, setBusy] = useState(null)
  const [deleteID, setDeleteID] = useState(null)
  const [logView, setLogView] = useState(null)
  const [loading, setLoading] = useState(true)

  const refresh = useCallback((quiet = false) => {
    if (!quiet) setLoading(true)
    return api
      .get(`${PLUGIN_BASE}/status`)
      .then((data) => {
        setOverview(data)
        if (!quiet) {
          setAccountForm({
            Email: data.Account?.Email || '',
            Provider: data.Account?.Provider || '',
            CA: data.Account?.CA || 'staging',
            AcceptTOS: !!data.Account?.AcceptTOS
          })
        }
      })
      .catch((err) => {
        if (!quiet) alert.error('Failed to load spr-acme', err)
      })
      .finally(() => {
        if (!quiet) setLoading(false)
      })
  }, [alert])

  useEffect(() => {
    refresh()
    api.get(`${PLUGIN_BASE}/providers`).then(setProviderCodes).catch(() => {})
    const timer = setInterval(() => refresh(true), 5000)
    return () => clearInterval(timer)
  }, [refresh])

  useEffect(() => {
    const code = accountForm.Provider.trim().toLowerCase()
    if (!code || (providerCodes.length && !providerCodes.includes(code))) {
      setProviderInfo(null)
      return undefined
    }
    const timer = setTimeout(() => {
      api.get(`${PLUGIN_BASE}/providers/${code}`).then(setProviderInfo).catch(() => setProviderInfo(null))
    }, 250)
    return () => clearTimeout(timer)
  }, [accountForm.Provider, providerCodes])

  const account = overview?.Account || {}
  const certificates = overview?.Certificates || []
  const configuredCredentials = account.ConfiguredCredentials || []
  const accountReady = !!(account.Email && account.Provider && account.AcceptTOS)
  const issuedCount = certificates.filter((certificate) => certificate.Issued).length
  const runningCount = certificates.filter((certificate) => certificate.Job?.Running).length
  const errorCount = certificates.filter((certificate) => certificate.Job?.LastError || certificate.MappingSyncError).length

  const credentialFields = useMemo(() => {
    const fields = [...(providerInfo?.Credentials || []), ...(providerInfo?.Configuration || [])]
    const seen = new Set()
    return fields.filter((field) => {
      if (seen.has(field.Name)) return false
      seen.add(field.Name)
      return true
    })
  }, [providerInfo])

  const setProvider = (provider) => {
    setAccountForm({ ...accountForm, Provider: provider })
    setCredentialValues({})
    setClearCredentials([])
  }

  const setCredential = (name, value) => {
    setCredentialValues({ ...credentialValues, [name]: value })
    if (value) setClearCredentials(clearCredentials.filter((key) => key !== name))
  }

  const saveAccount = () => {
    const credentials = Object.fromEntries(
      Object.entries(credentialValues).filter(([, value]) => value !== '')
    )
    setBusy('account')
    api
      .put(`${PLUGIN_BASE}/account`, {
        ...accountForm,
        Provider: accountForm.Provider.trim().toLowerCase(),
        Credentials: credentials,
        ClearCredentials: clearCredentials
      })
      .then(() => {
        setCredentialValues({})
        setClearCredentials([])
        alert.success('ACME account settings saved')
        return refresh()
      })
      .catch((err) => alert.error('Could not save account settings', err))
      .finally(() => setBusy(null))
  }

  const resetProfileForm = () => {
    setEditingID(null)
    setProfileForm(emptyProfileForm())
  }

  const selectTab = (nextTab) => {
    if (nextTab === 'new-certificate' && tab !== 'new-certificate') resetProfileForm()
    setTab(nextTab)
  }

  const editProfile = (profile) => {
    setEditingID(profile.ID)
    setProfileForm(formFromProfile(profile))
    setTab('new-certificate')
  }

  const saveProfile = () => {
    let payload
    try {
      payload = profileFromForm(profileForm)
    } catch (err) {
      alert.error('Invalid certificate profile', err)
      return
    }
    setBusy('profile')
    const request = editingID
      ? api.put(`${PLUGIN_BASE}/certificates/${editingID}`, payload)
      : api.post(`${PLUGIN_BASE}/certificates`, payload)
    request
      .then((result) => {
        if (result.SyncError) alert.error('Saved, but local DNS sync failed', new Error(result.SyncError))
        else alert.success(editingID ? 'Certificate profile updated' : 'Certificate profile created')
        resetProfileForm()
        setTab('certificates')
        return refresh(true)
      })
      .catch((err) => alert.error('Could not save certificate profile', err))
      .finally(() => setBusy(null))
  }

  const issue = (certificate) => {
    setBusy(`issue:${certificate.Profile.ID}`)
    api
      .post(`${PLUGIN_BASE}/certificates/${certificate.Profile.ID}/issue?force=${certificate.Issued ? '1' : '0'}`, {})
      .then(() => {
        alert.success(certificate.Issued ? 'Forced renewal started' : 'Certificate issuance started')
        return refresh(true)
      })
      .catch((err) => alert.error('Could not start certificate operation', err))
      .finally(() => setBusy(null))
  }

  const syncDNS = (id) => {
    setBusy(`sync:${id}`)
    api
      .post(`${PLUGIN_BASE}/certificates/${id}/sync`, {})
      .then(() => {
        alert.success('Local DNS mappings synchronized')
        return refresh(true)
      })
      .catch((err) => alert.error('Local DNS sync failed', err))
      .finally(() => setBusy(null))
  }

  const removeProfile = () => {
    const id = deleteID
    setDeleteID(null)
    setBusy(`delete:${id}`)
    api
      .delete(`${PLUGIN_BASE}/certificates/${id}`, {})
      .then((result) => {
        if (result.SyncError) alert.error('Profile removed, but DNS cleanup failed', new Error(result.SyncError))
        else alert.success('Certificate profile removed; certificate files were retained')
        return refresh(true)
      })
      .catch((err) => alert.error('Could not remove certificate profile', err))
      .finally(() => setBusy(null))
  }

  const showLog = (id) => {
    api
      .get(`${PLUGIN_BASE}/certificates/${id}/log`)
      .then((result) => setLogView({ id, output: result.Output || 'No operation log yet.' }))
      .catch((err) => alert.error('Could not load operation log', err))
  }

  const download = (id, name) => {
    window.open(`${PLUGIN_BASE}/certificates/${id}/files/${name}`, '_blank', 'noopener,noreferrer')
  }

  if (loading) return <Page><Loading /></Page>
  if (!overview) {
    return (
      <Page>
        <EmptyState title="spr-acme is unreachable" description="Check that the plugin container is running, then retry.">
          <Button onPress={() => refresh()}><ButtonText>Retry</ButtonText></Button>
        </EmptyState>
      </Page>
    )
  }

  const overviewTab = (
    <VStack space="lg">
      <HStack flexWrap="wrap" gap="$3">
        <StatTile label="ACME account" value={accountReady ? 'Ready' : 'Setup needed'} />
        <StatTile label="Certificates" value={`${issuedCount} issued / ${certificates.length} profiles`} />
        <StatTile label="Operations" value={runningCount ? `${runningCount} running` : 'Idle'} />
        <StatTile label="Alerts" value={errorCount ? `${errorCount} need attention` : 'None'} />
      </HStack>

      <Card p="$5">
        <SectionHeader title="How local trusted HTTPS works" description="Public trust, private routing" />
        <VStack space="md" mt="$4">
          <InfoRow label="1 · Prove domain control" value="lego creates a temporary public DNS TXT record through your provider API." />
          <InfoRow label="2 · Issue without inbound ports" value="Let's Encrypt validates DNS-01; no public web server, port forward, or public A record is required." />
          <InfoRow label="3 · Route only inside SPR" value="SPR CoreDNS maps each configured hostname to its private LAN address." />
          <InfoRow label="4 · Mount the files" value="Services read the stable exported certificate and key paths and reload after renewal." />
        </VStack>
      </Card>

      <Card tone="warning" p="$5">
        <VStack space="sm">
          <Text fontWeight="$semibold">Public certificate transparency</Text>
          <Text size="sm" color="$muted600">
            Let's Encrypt certificates are publicly logged. Hostnames in a certificate are discoverable even when they resolve only on your local network. Prefer a non-sensitive subdomain and a wildcard when appropriate.
          </Text>
        </VStack>
      </Card>

      {!accountReady ? (
        <Card p="$5">
          <SectionHeader title="Start with your DNS provider" description="Use a narrowly scoped token that can edit TXT records in only the required zone." />
          <Button mt="$4" size="sm" alignSelf="flex-start" onPress={() => setTab('account')}>
            <ButtonText>Configure account</ButtonText>
          </Button>
        </Card>
      ) : null}
    </VStack>
  )

  const accountTab = (
    <VStack space="lg">
      <Card p="$5">
        <SectionHeader title="Let's Encrypt account" description="DNS-01 can issue trusted certificates for services that never face the internet." />
        <VStack space="md" mt="$4">
          <TextField
            label="Account email"
            value={accountForm.Email}
            onChangeText={(Email) => setAccountForm({ ...accountForm, Email })}
            placeholder="admin@example.com"
            helper="Let's Encrypt uses this for important account and expiration notices."
          />
          <VStack space="sm">
            <Label>Certificate authority</Label>
            <Segment
              value={accountForm.CA}
              onChange={(CA) => setAccountForm({ ...accountForm, CA })}
              options={[{ value: 'staging', label: 'Staging (test)' }, { value: 'production', label: 'Production' }]}
            />
            <Text size="xs" color="$muted500">
              Start on staging to verify credentials without consuming production rate limits. Staging certificates are not trusted by browsers.
            </Text>
          </VStack>
          <HStack justifyContent="space-between" alignItems="center" space="md">
            <VStack flex={1} space="xs">
              <Text size="sm" fontWeight="$medium">Accept CA terms of service</Text>
              <Text size="xs" color="$muted500">Required before lego can register the account or issue certificates.</Text>
            </VStack>
            <Toggle
              value={accountForm.AcceptTOS}
              onPress={() => setAccountForm({ ...accountForm, AcceptTOS: !accountForm.AcceptTOS })}
              label="Accept terms"
            />
          </HStack>
        </VStack>
      </Card>

      <Card p="$5">
        <SectionHeader title="DNS provider" description="All lego DNS integrations are available; common choices are one click away." />
        <VStack space="md" mt="$4">
          <HStack flexWrap="wrap" gap="$2">
            {COMMON_PROVIDERS.map(([code, label]) => (
              <Button key={code} size="xs" variant={accountForm.Provider === code ? 'solid' : 'outline'} onPress={() => setProvider(code)}>
                <ButtonText>{label}</ButtonText>
              </Button>
            ))}
          </HStack>
          <TextField
            label="lego provider code"
            value={accountForm.Provider}
            onChangeText={setProvider}
            placeholder="cloudflare"
            helper={providerCodes.length ? `${providerCodes.length} safe automated providers available; manual and executable providers are intentionally disabled.` : 'Enter the provider code from lego DNS documentation.'}
          />
          {providerInfo ? (
            <Box p="$4" borderRadius="$xl" bg="$backgroundContentLight" sx={{ _dark: { bg: '$backgroundContentDark' } }}>
              <VStack space="xs">
                <Text fontWeight="$semibold">{providerInfo.Name}</Text>
                <Mono size="xs">{providerInfo.Documentation}</Mono>
              </VStack>
            </Box>
          ) : null}
        </VStack>
      </Card>

      {credentialFields.length ? (
        <Card p="$5">
          <SectionHeader title="Provider credentials" description="Blank fields keep saved values. Secrets are never returned by the API or shown again." />
          <VStack space="md" mt="$4">
            {credentialFields.map((field) => {
              const configured = configuredCredentials.includes(field.Name) && !clearCredentials.includes(field.Name)
              return (
                <VStack key={field.Name} space="xs">
                  <TextField
                    label={field.Name}
                    value={credentialValues[field.Name] || ''}
                    onChangeText={(value) => setCredential(field.Name, value)}
                    secureTextEntry
                    placeholder={configured ? 'Saved — enter a replacement' : 'Enter value'}
                    helper={field.Description}
                  />
                  {configured ? (
                    <HStack alignItems="center" space="sm">
                      <Badge action="success" variant="outline"><BadgeText>Configured</BadgeText></Badge>
                      <Button size="xs" variant="link" action="negative" onPress={() => setClearCredentials([...clearCredentials, field.Name])}>
                        <ButtonText>Remove saved value</ButtonText>
                      </Button>
                    </HStack>
                  ) : null}
                  {clearCredentials.includes(field.Name) ? <Text size="xs" color="$red600">This saved value will be removed.</Text> : null}
                </VStack>
              )
            })}
          </VStack>
        </Card>
      ) : null}

      <Button alignSelf="flex-start" onPress={saveAccount} isDisabled={busy === 'account'}>
        <ButtonText>{busy === 'account' ? 'Saving…' : 'Save account settings'}</ButtonText>
      </Button>
    </VStack>
  )

  const certificatesTab = (
    <VStack space="lg">
      {!accountReady ? (
        <Card tone="warning" p="$4">
          <Text size="sm">Configure the account, DNS provider, credentials, and terms before issuing a certificate.</Text>
        </Card>
      ) : null}

      {certificates.map((certificate) => {
        const profile = certificate.Profile
        const state = certificateState(certificate)
        return (
          <Card key={profile.ID} p="$5">
            <VStack space="md">
              <HStack justifyContent="space-between" alignItems="flex-start" flexWrap="wrap" gap="$3">
                <HStack space="md" alignItems="center">
                  <StatusDot online={certificate.Issued && !certificate.Job?.LastError} />
                  <VStack>
                    <Text fontWeight="$semibold" size="md">{profile.Name}</Text>
                    <Mono size="xs">{profile.ID}</Mono>
                  </VStack>
                </HStack>
                <Badge action={state.action} variant="outline"><BadgeText>{state.label}</BadgeText></Badge>
              </HStack>
              <InfoRow label="Names" value={(profile.Domains || []).join(', ')} mono />
              <InfoRow label="Expires" value={certificate.Issued ? formatDate(certificate.NotAfter) : 'Not issued'} />
              <InfoRow label="Automatic renewal" value={profile.AutoRenew ? `${profile.RenewBeforeDays} days before expiry` : 'Off'} />
              <InfoRow label="Local DNS" value={profile.Mappings?.length ? `${profile.Mappings.length} split-horizon mappings` : 'No mappings'} />
              {certificate.Job?.LastError ? <Text size="xs" color="$red600">{certificate.Job.LastError}</Text> : null}
              {certificate.MappingSyncError ? <Text size="xs" color="$orange600">DNS: {certificate.MappingSyncError}</Text> : null}
              <HStack flexWrap="wrap" gap="$2">
                <Button size="xs" onPress={() => issue(certificate)} isDisabled={!accountReady || certificate.Job?.Running || busy === `issue:${profile.ID}`}>
                  <ButtonText>{certificate.Issued ? 'Renew now' : 'Issue certificate'}</ButtonText>
                </Button>
                <Button size="xs" variant="outline" onPress={() => editProfile(profile)}><ButtonText>Edit</ButtonText></Button>
                <Button size="xs" variant="outline" onPress={() => syncDNS(profile.ID)} isDisabled={busy === `sync:${profile.ID}`}><ButtonText>Sync DNS</ButtonText></Button>
                <Button size="xs" variant="outline" onPress={() => showLog(profile.ID)}><ButtonText>Log</ButtonText></Button>
                <Button size="xs" variant="outline" action="negative" onPress={() => setDeleteID(profile.ID)}><ButtonText>Remove</ButtonText></Button>
              </HStack>
              {certificate.Issued ? (
                <VStack space="sm" pt="$2">
                  <Label>Download files</Label>
                  <HStack flexWrap="wrap" gap="$2">
                    {['fullchain.pem', 'cert.pem', 'chain.pem', 'privkey.pem'].map((name) => (
                      <Button key={name} size="xs" variant="link" onPress={() => download(profile.ID, name)}>
                        <ButtonText>{name}</ButtonText>
                      </Button>
                    ))}
                  </HStack>
                  <Mono size="xs">{certificate.ExportPath}</Mono>
                </VStack>
              ) : null}
            </VStack>
          </Card>
        )
      })}

      {!certificates.length ? (
        <EmptyState title="No certificate profiles" description="Create one certificate for a service or a wildcard certificate for a private subdomain.">
          <Button onPress={() => selectTab('new-certificate')}><ButtonText>New certificate</ButtonText></Button>
        </EmptyState>
      ) : null}
    </VStack>
  )

  const newCertificateTab = (
    <VStack space="lg">
      {!accountReady ? (
        <Card tone="warning" p="$4">
          <Text size="sm">Configure the account, DNS provider, credentials, and terms before issuing this certificate.</Text>
        </Card>
      ) : null}
      <Card p="$5">
        <SectionHeader
          title={editingID ? `Edit ${editingID}` : 'New certificate'}
          description="Certificate names must belong to a real public domain. Local mappings must point to private LAN addresses and be covered by the certificate."
        />
        <VStack space="md" mt="$4">
          <TextField
            label="ID"
            value={profileForm.ID}
            onChangeText={(ID) => setProfileForm({ ...profileForm, ID })}
            isDisabled={!!editingID}
            placeholder="home-services"
            helper="Stable file/directory name: lowercase letters, digits, and hyphens."
          />
          <TextField label="Display name" value={profileForm.Name} onChangeText={(Name) => setProfileForm({ ...profileForm, Name })} placeholder="Home services" />
          <VStack space="xs">
            <Label>Certificate DNS names</Label>
            <Textarea h="$24">
              <TextareaInput
                value={profileForm.Domains}
                onChangeText={(Domains) => setProfileForm({ ...profileForm, Domains })}
                placeholder={'*.home.example.com\nhome.example.com'}
                sx={{ '@base': { fontFamily: 'monospace' } }}
              />
            </Textarea>
            <Text size="xs" color="$muted500">One per line or comma-separated. DNS-01 supports wildcard certificates.</Text>
          </VStack>
          <VStack space="sm">
            <Label>Private key type</Label>
            <Segment
              value={profileForm.KeyType}
              onChange={(KeyType) => setProfileForm({ ...profileForm, KeyType })}
              options={[{ value: 'EC256', label: 'ECDSA P-256' }, { value: 'EC384', label: 'ECDSA P-384' }, { value: 'RSA2048', label: 'RSA 2048' }, { value: 'RSA4096', label: 'RSA 4096' }]}
            />
          </VStack>
          <HStack justifyContent="space-between" alignItems="center" space="md">
            <VStack flex={1} space="xs">
              <Text size="sm" fontWeight="$medium">Automatic renewal</Text>
              <Text size="xs" color="$muted500">spr-acme checks twice daily; lego renews only inside the configured window.</Text>
            </VStack>
            <Toggle value={profileForm.AutoRenew} onPress={() => setProfileForm({ ...profileForm, AutoRenew: !profileForm.AutoRenew })} label="Automatic renewal" />
          </HStack>
          <TextField
            label="Renew before expiry (days)"
            value={profileForm.RenewBeforeDays}
            onChangeText={(RenewBeforeDays) => setProfileForm({ ...profileForm, RenewBeforeDays })}
            keyboardType="numeric"
            helper="Between 7 and 60 days; 30 is a safe default."
          />
          <VStack space="xs">
            <Label>SPR local DNS mappings (optional)</Label>
            <Textarea h="$24">
              <TextareaInput
                value={profileForm.Mappings}
                onChangeText={(Mappings) => setProfileForm({ ...profileForm, Mappings })}
                placeholder={'vault.home.example.com 192.168.2.20\nha.home.example.com 192.168.2.30'}
                sx={{ '@base': { fontFamily: 'monospace' } }}
              />
            </Textarea>
            <Text size="xs" color="$muted500">One “hostname private-IP” pair per line. CoreDNS reloads mappings automatically.</Text>
          </VStack>
          <HStack space="sm">
            <Button onPress={saveProfile} isDisabled={busy === 'profile'}><ButtonText>{busy === 'profile' ? 'Saving…' : editingID ? 'Save profile' : 'Create profile'}</ButtonText></Button>
            {editingID ? <Button variant="outline" onPress={() => { resetProfileForm(); setTab('certificates') }}><ButtonText>Cancel</ButtonText></Button> : null}
          </HStack>
        </VStack>
      </Card>
    </VStack>
  )

  return (
    <Page>
      <ListHeader
        title="ACME Certificates"
        description="Trusted Let's Encrypt certificates for private SPR services"
        mark="AC"
        status={accountReady ? `${account.Provider} · ${account.CA}` : 'Setup needed'}
        statusAction={accountReady ? 'success' : 'warning'}
      >
        <Badge action="muted" variant="outline"><BadgeText>lego {overview.LegoVersion}</BadgeText></Badge>
      </ListHeader>

      <Box mb="$5">
        <Segment
          value={tab}
          onChange={selectTab}
          options={[
            { value: 'overview', label: 'Overview' },
            { value: 'account', label: 'Account & DNS' },
            { value: 'certificates', label: `Certificates (${certificates.length})` },
            { value: 'new-certificate', label: editingID ? 'Edit certificate' : 'New certificate' }
          ]}
        />
      </Box>

      {tab === 'overview' ? overviewTab : null}
      {tab === 'account' ? accountTab : null}
      {tab === 'certificates' ? certificatesTab : null}
      {tab === 'new-certificate' ? newCertificateTab : null}

      <ModalConfirm
        isOpen={!!deleteID}
        onClose={() => setDeleteID(null)}
        onConfirm={removeProfile}
        title="Remove certificate profile?"
        message="The profile and its SPR local DNS mappings are removed. Existing certificate files are retained so services do not break unexpectedly."
        confirmText="Remove profile"
        destructive
      />
      <ModalConfirm
        isOpen={!!logView}
        onClose={() => setLogView(null)}
        onConfirm={() => setLogView(null)}
        title={logView ? `Operation log · ${logView.id}` : 'Operation log'}
        message={logView?.output || ''}
        confirmText="Close"
      />
    </Page>
  )
}
