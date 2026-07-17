export const emptyProfileForm = () => ({
  ID: '',
  Name: '',
  Domains: '',
  KeyType: 'EC256',
  AutoRenew: true,
  RenewBeforeDays: '30',
  Mappings: ''
})

export const formFromProfile = (profile) => ({
  ID: profile?.ID || '',
  Name: profile?.Name || '',
  Domains: (profile?.Domains || []).join('\n'),
  KeyType: profile?.KeyType || 'EC256',
  AutoRenew: profile?.AutoRenew !== false,
  RenewBeforeDays: String(profile?.RenewBeforeDays || 30),
  Mappings: (profile?.Mappings || [])
    .map((mapping) => `${mapping.Hostname} ${mapping.IPAddress}`)
    .join('\n')
})

export const parseDomains = (value) =>
  (value || '')
    .split(/[\n,]/)
    .map((domain) => domain.trim().toLowerCase())
    .filter(Boolean)

export const parseMappings = (value) =>
  (value || '')
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)
    .map((line) => {
      const fields = line.split(/\s+/)
      if (fields.length !== 2) {
        throw new Error(`Mapping must be "hostname IP": ${line}`)
      }
      return { Hostname: fields[0].toLowerCase(), IPAddress: fields[1] }
    })

export const profileFromForm = (form) => ({
  ID: form.ID.trim().toLowerCase(),
  Name: form.Name.trim(),
  Domains: parseDomains(form.Domains),
  KeyType: form.KeyType,
  AutoRenew: !!form.AutoRenew,
  RenewBeforeDays: Number(form.RenewBeforeDays),
  Mappings: parseMappings(form.Mappings)
})
