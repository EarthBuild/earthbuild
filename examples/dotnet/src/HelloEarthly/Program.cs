using System;
using NodaTime;

namespace Helloearthbuild
{
    class Program
    {
        static void Main(string[] args)
        {
            Console.WriteLine("Hello World! Its " + SystemClock.Instance.GetCurrentInstant());
        }
    }
}
