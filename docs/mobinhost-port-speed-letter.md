# نامه استعلام سرعت پورت شبکه — مبین‌هاست

**برای:** پشتیبانی مبین‌هاست
**درباره:** سرور `vm37438-55060.mobinhost.com` / `87.107.110.199`
**تاریخ:** ۱۴۰۵/۰۶/۰۲

> متن زیر آمادهٔ ارسال است. فقط نام خود را در انتها جایگزین کنید.
> توضیح انگلیسی هر پرسش در پایین همین فایل آمده و **بخشی از نامه نیست**.

---

## متن نامه

**موضوع:** استعلام سرعت پورت شبکه سرور مجازی — `vm37438-55060`

با سلام و احترام،

اینجانب مالک سرور مجازی زیر هستم و خواهشمند است در خصوص مشخصات شبکهٔ آن راهنمایی بفرمایید:

- **نام سرور:** `vm37438-55060.mobinhost.com`
- **آدرس آی‌پی:** `87.107.110.199`

در حال برنامه‌ریزی برای راه‌اندازی سرویسی هستیم که ترافیک **پایدار و مداوم** خواهد داشت، و پیش از آغاز به کار، اطمینان از ظرفیت پورت شبکه برای ما ضروری است. لازم به توضیح است که ابزارهای داخل سیستم‌عامل قادر به تشخیص سرعت پورت نیستند و به دلیل استفاده از درایور `virtio` مقدار نامعتبر گزارش می‌کنند؛ از این رو ناچار به استعلام از جنابعالی هستیم.

خواهشمند است موارد زیر را اعلام فرمایید:

**۱.** سرعت پورت شبکهٔ تخصیص‌یافته به این سرور چقدر است؟ (برای نمونه ۱۰۰ مگابیت بر ثانیه، ۲۰۰ مگابیت بر ثانیه، یا ۱ گیگابیت بر ثانیه)

**۲.** آیا این پورت به‌صورت **اختصاصی** در اختیار این سرور قرار دارد، یا پهنای باند آن با سرورهای دیگر روی همان میزبان به اشتراک گذاشته می‌شود؟

**۳.** آیا محدودیتی در **حجم ترافیک ماهانه** وجود دارد؟ در صورت وجود، پس از عبور از آن حد، سرعت کاهش می‌یابد یا هزینهٔ اضافه محاسبه می‌شود؟

**۴.** سرعت اعلام‌شده برای ترافیک **ورودی و خروجی یکسان** است یا برای هر جهت مقدار متفاوتی در نظر گرفته شده است؟

**۵.** در صورت نیاز به **ارتقای سرعت پورت به ۱ گیگابیت بر ثانیه**، آیا این امکان روی همین سرور وجود دارد و هزینهٔ ماهانهٔ آن چقدر خواهد بود؟

برای شفافیت بیشتر عرض می‌کنم که مصرف پیش‌بینی‌شدهٔ ما در زمان اوج، حدود **۱۰۰ مگابیت بر ثانیه در هر جهت** به‌صورت هم‌زمان و پایدار است. چنانچه پورت فعلی پاسخگوی این میزان نباشد، ترجیح می‌دهیم پیش از راه‌اندازی سرویس نسبت به ارتقای آن یا انتخاب سرور مناسب‌تری اقدام کنیم.

پیشاپیش از توجه و همکاری شما سپاسگزارم.

با تشکر و احترام،
**[نام شما]**

---

## What this letter asks (for you, not for them)

Five questions, in the order they appear:

1. **What is the port speed?** The one number the whole capacity plan is missing.
2. **Is it dedicated or shared?** A shared 1 Gbps port among busy neighbours can
   be worse in practice than a dedicated 200 Mbps one.
3. **Is there a monthly traffic cap, and what happens past it?** Throttling and
   overage billing are very different problems. Traffic is the dominant cost of
   this business, so this matters as much as the speed.
4. **Is upload the same as download?** Our relay sends roughly as much as it
   receives. A package advertised on its download figure with a much smaller
   upload would be a real problem, and it is a common arrangement.
5. **Can it go to 1 Gbps, and at what price?** So you can decide with a number
   in front of you rather than in the abstract.

**Why 100 Mbps is the figure quoted.** We measured 500 synthetic players against
the real relay at 47.8 Mbps each way. Real Dota packets run larger than the
synthetic ones, so the same player count lands nearer 100 Mbps. Quoting the
higher number means their answer is useful either way.

**If they answer 100 Mbps or less:** that is the trigger to buy the dedicated
server rather than upgrade this one, since D47 already puts that purchase before
public launch.
