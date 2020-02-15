

def do_one():
    url = 'https://api.stripe.com/v1/tokens'
    payload = {
        "card[number]": '4242424242424242',
        "card[exp_month]": 2,
        "card[exp_year]": 2021,
        "card[cvc]": 314,
    }
    headers = {
        'authorization': 'Bearer sk_test_blurp',
    }
    r = requests.post(url, data=payload, headers=headers)

    # r = requests.get('https://github.com/timeline.json')
    print('r.status_code: %s (%s)' % (r.status_code, r.text))
    # print('r.status_code: %s (%s)' % (r.status_code, json.decode(r.text)['message']))
    # we expect an HTTP 410
    # r.raise_for_status()

def main(ctx):
    do_one()
