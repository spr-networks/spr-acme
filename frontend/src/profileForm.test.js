import { formFromProfile, parseDomains, parseMappings, profileFromForm } from './profileForm'

test('parses domains and local mappings', () => {
  expect(parseDomains('*.home.example.com, home.example.com\n')).toEqual([
    '*.home.example.com',
    'home.example.com'
  ])
  expect(parseMappings('vault.home.example.com 192.168.2.20')).toEqual([
    { Hostname: 'vault.home.example.com', IPAddress: '192.168.2.20' }
  ])
})

test('rejects malformed mapping lines', () => {
  expect(() => parseMappings('missing-address.example.com')).toThrow(/hostname IP/)
})

test('round trips a profile through the form', () => {
  const profile = {
    ID: 'home',
    Name: 'Home services',
    Domains: ['*.home.example.com'],
    KeyType: 'EC384',
    AutoRenew: true,
    RenewBeforeDays: 21,
    Mappings: [{ Hostname: 'vault.home.example.com', IPAddress: '192.168.2.20' }]
  }
  expect(profileFromForm(formFromProfile(profile))).toEqual(profile)
})
